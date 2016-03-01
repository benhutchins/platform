package util

import (
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path"
	"strconv"
	"time"

	"github.com/goodeggs/platform/cmd/ranch/Godeps/_workspace/src/github.com/convox/rack/client"
	"github.com/goodeggs/platform/cmd/ranch/Godeps/_workspace/src/github.com/jhoonb/archivex"
	"github.com/goodeggs/platform/cmd/ranch/Godeps/_workspace/src/github.com/spf13/viper"
)

func convoxClient() (*client.Client, error) {
	if !viper.IsSet("convox.host") || !viper.IsSet("convox.password") {
		return nil, fmt.Errorf("must set 'convox.host' and 'convox.password' in $HOME/.ranch.yaml")
	}

	host := viper.GetString("convox.host")
	password := viper.GetString("convox.password")
	version := "20151211151200"
	return client.New(host, password, version), nil
}

func ConvoxReleases(appName string) (Releases, error) {
	client, err := convoxClient()

	if err != nil {
		return nil, err
	}

	app, err := client.GetApp(appName)

	if err != nil {
		return nil, err
	}

	convoxReleases, err := client.GetReleases(appName)

	if err != nil {
		return nil, err
	}

	shaMap, err := buildShaMap(appName)

	if err != nil {
		return nil, err
	}

	var releases Releases

	for _, convoxRelease := range convoxReleases {
		status := ""

		if app.Release == convoxRelease.Id {
			status = "active"
		}

		appVersion, ok := shaMap[convoxRelease.Id]

		if !ok {
			continue
		}

		release := Release{
			Id:      appVersion,
			App:     appName,
			Created: convoxRelease.Created,
			Status:  status,
		}

		releases = append(releases, release)
	}

	return releases, nil
}

func ConvoxRunDetached(appName, process, command string) error {
	client, err := convoxClient()

	if err != nil {
		return err
	}

	return client.RunProcessDetached(appName, process, command)
}

func ConvoxRunAttached(appName, process, command string, input io.Reader, output io.WriteCloser) (int, error) {
	client, err := convoxClient()

	if err != nil {
		return -1, err
	}

	return client.RunProcessAttached(appName, process, command, input, output)
}

func ConvoxLogs(appName string, output io.WriteCloser) error {
	client, err := convoxClient()

	if err != nil {
		return err
	}

	return client.StreamAppLogs(appName, output)
}

func ConvoxGetFormation(appName string) (formation RanchFormation, err error) {

	formation = make(RanchFormation)

	client, err := convoxClient()

	if err != nil {
		return nil, err
	}

	convoxFormation, err := client.ListFormation(appName)

	if err != nil {
		return nil, err
	}

	for _, convoxFormationEntry := range convoxFormation {
		formation[convoxFormationEntry.Name] = RanchFormationEntry{
			Instances: convoxFormationEntry.Count,
			Memory:    convoxFormationEntry.Memory,
			Balancer:  convoxFormationEntry.Balancer,
		}
	}

	return formation, nil
}

func ConvoxScaleProcess(appName, processName string, instances, memory int) (err error) {
	client, err := convoxClient()

	if err != nil {
		return err
	}

	message := fmt.Sprintf("scaling %s to", processName)
	if instances > -1 {
		message += fmt.Sprintf(" instances=%d", instances)
	}
	if memory > -1 {
		message += fmt.Sprintf(" memory=%d", memory)
	}
	fmt.Println(message)

	strInstances := ""
	if instances != -1 {
		strInstances = strconv.Itoa(instances)
	}

	strMemory := ""
	if memory != -1 {
		strMemory = strconv.Itoa(memory)
	}

	if err = client.SetFormation(appName, processName, strInstances, strMemory); err != nil {
		return err
	}

	return nil
}

func ConvoxScale(appName string, config *RanchConfig) (err error) {

	existingFormation, err := ConvoxGetFormation(appName)

	if err != nil {
		return err
	}

	for processName, processConfig := range config.Processes {
		if existingEntry, ok := existingFormation[processName]; ok {
			if existingEntry.Instances == processConfig.Instances && existingEntry.Memory == processConfig.Memory {
				fmt.Printf("%s already scaled to instances=%d memory=%d\n", processName, processConfig.Instances, processConfig.Memory)
				continue
			}
		}

		if err = ConvoxScaleProcess(appName, processName, processConfig.Instances, processConfig.Memory); err != nil {
			return err
		}
	}

	return nil
}

func ConvoxPromote(appName string, appVersion string) error {
	releaseId, err := getConvoxRelease(appName, appVersion)

	if err != nil {
		return err
	}

	client, err := convoxClient()

	if err != nil {
		return err
	}

	_, err = client.PromoteRelease(appName, releaseId)

	return err
}

func ConvoxDeploy(appName string, buildDir string) (string, error) {
	client, err := convoxClient()

	if err != nil {
		return "", err
	}

	app, err := client.GetApp(appName)

	if err != nil {
		return "", err
	}

	switch app.Status {
	case "creating":
		return "", fmt.Errorf("app is still creating: %s", appName)
	case "running", "updating":
	default:
		return "", fmt.Errorf("unable to build app: %s", appName)
	}

	tar, err := createTarball(buildDir)

	if err != nil {
		return "", err
	}

	cache := true
	config := "docker-compose.yml"

	build, err := client.CreateBuildSource(appName, tar, cache, config)

	if err != nil {
		return "", err
	}

	return finishBuild(client, appName, build)
}

func createTarball(buildDir string) ([]byte, error) {
	tmpDir, err := ioutil.TempDir("", "ranch")
	defer os.RemoveAll(tmpDir)
	fmt.Println(tmpDir)

	if err != nil {
		return nil, err
	}

	tgzfile := path.Join(tmpDir, "build.tar.gz")

	tar := new(archivex.TarFile)

	err = tar.Create(tgzfile)
	if err != nil {
		return nil, err
	}

	err = tar.AddAll(buildDir, false)
	if err != nil {
		return nil, err
	}

	err = tar.Close()
	if err != nil {
		return nil, err
	}

	return ioutil.ReadFile(tgzfile)
}

func finishBuild(client *client.Client, appName string, build *client.Build) (string, error) {

	if build.Id == "" {
		return "", fmt.Errorf("unable to fetch build id")
	}

	reader, writer := io.Pipe()
	go io.Copy(os.Stdout, reader)
	err := client.StreamBuildLogs(appName, build.Id, writer)

	if err != nil {
		return "", err
	}

	return waitForBuild(client, appName, build.Id)
}

func waitForBuild(client *client.Client, appName, buildId string) (string, error) {
	for {
		build, err := client.GetBuild(appName, buildId)

		if err != nil {
			return "", err
		}

		switch build.Status {
		case "complete":
			return build.Release, nil
		case "error":
			return "", fmt.Errorf("%s build failed", appName)
		case "failed":
			return "", fmt.Errorf("%s build failed", appName)
		}

		time.Sleep(1 * time.Second)
	}

	return "", fmt.Errorf("can't get here")
}

func ConvoxWaitForStatus(appName, status string) error {
	client, err := convoxClient()

	if err != nil {
		return err
	}

	fmt.Printf("waiting for '%s' status", status)

	for {
		app, err := client.GetApp(appName)

		if err != nil {
			fmt.Println(" ERROR")
			return err
		}

		if app.Status == status {
			fmt.Println(" OK")
			return nil
		}

		fmt.Print(".")
		time.Sleep(5 * time.Second)
	}

	return fmt.Errorf("can't get here")
}

func ConvoxPs(appName string) (Processes, error) {
	client, err := convoxClient()

	if err != nil {
		return nil, err
	}

	convoxPs, err := client.GetProcesses(appName, false) // false == no process stats

	if err != nil {
		return nil, err
	}

	shaMap, err := buildShaMap(appName)

	if err != nil {
		return nil, err
	}

	var ps Processes

	for _, v := range convoxPs {
		p := Process(v)

		sha, ok := shaMap[p.Release]

		if !ok {
			p.Release = "convox:" + p.Release
		} else {
			p.Release = sha
		}

		ps = append(ps, p)
	}

	return ps, nil
}

func buildShaMap(appName string) (map[string]string, error) {
	ecruReleases, err := EcruReleases(appName)

	if err != nil {
		return nil, err
	}

	shaMap := make(map[string]string)

	for _, ecruRelease := range ecruReleases {
		shaMap[ecruRelease.ConvoxRelease] = ecruRelease.Sha
	}

	return shaMap, nil
}

func getConvoxRelease(appName, appVersion string) (releaseId string, err error) {
	ecruReleases, err := EcruReleases(appName)

	if err != nil {
		return "", err
	}

	for _, ecruRelease := range ecruReleases {
		if ecruRelease.Sha == appVersion {
			return ecruRelease.ConvoxRelease, nil
		}
	}

	return "", fmt.Errorf("could not find Convox release for sha " + appVersion)
}
