package util

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"net/url"
	"os"
	"path"
	"regexp"
	"strings"
	"text/template"
	"time"

	"github.com/goodeggs/platform/cmd/ranch/Godeps/_workspace/src/github.com/parnurzeal/gorequest"
	"github.com/goodeggs/platform/cmd/ranch/Godeps/_workspace/src/github.com/spf13/viper"
)

var cedarDockerfileTemplate = template.Must(template.New("cedar-dockerfile").Parse(`# generated by ranch
FROM goodeggs/cedar:5c11670
MAINTAINER Good Eggs <open-source@goodeggs.com>
`))

var nodejsDockerfileTemplate = template.Must(template.New("nodejs-dockerfile").Parse(`# generated by ranch
FROM goodeggs/ranch-baseimage-nodejs:659a9ae
MAINTAINER Good Eggs <open-source@goodeggs.com>
`))

type composeTemplateVars struct {
	ImageName   string
	Environment map[string]string
	Config      *RanchConfig
}

var dockerComposeTemplate = template.Must(template.New("docker-compose").Parse(`# generated by ranch
{{ range $name, $process := .Config.Processes }}
{{ $name }}:
  image: {{ $.ImageName }}
  command: /start {{ $process.Command }}
  volumes:
    - /var/run/docker.sock:/var/run/docker.sock
  {{ if eq $name "web" }}
  labels:
    - convox.port.443.protocol=https
    - convox.idle.timeout=60
  ports:
    - 443:3000
  {{ end }}
  environment:
{{ if eq $name "web" }}{{printf "    - PORT=3000\n" }}{{ end }}
{{ range $k, $v := $.Environment }}{{ printf "    - %s=%s\n" $k $v }}{{ end }}
{{ end }}

run:
  image: {{ $.ImageName }}
  command: sh -c 'while true; do echo this process should not be running; sleep 300; done'
  volumes:
    - /var/run/docker.sock:/var/run/docker.sock
  labels:
{{ range $k, $v := $.Config.Cron }}{{ printf "    - convox.cron.%s=%s\n" $k $v }}{{ end }}
  environment:
{{ range $k, $v := $.Environment }}{{ printf "    - %s=%s\n" $k $v }}{{ end }}
`))

type RanchApiError struct {
	Message string `json:"error"`
}

type RanchApiRelease struct {
	Id            string `json:"id"` // sha
	App           string `json:"app"`
	ConvoxRelease string `json:"convoxRelease"`
}

type RanchApiSecret struct {
	Id      string `json:"_id"`
	Content string `json:"content"`
}

type RanchConfig struct {
	AppName   string                        `json:"name"`
	ImageName string                        `json:"image_name"`
	EnvId     string                        `json:"env_id"`
	Processes map[string]RanchConfigProcess `json:"processes"`
	Cron      map[string]string             `json:"cron"`
}

type RanchConfigProcess struct {
	Command   string `json:"command"`
	Count     int    `json:"count"`
	Instances int    `json:"instances"` // deprecated
	Memory    int    `json:"memory"`
}

type RanchFormationEntry struct {
	Name     string `json:"name"`
	Balancer string `json:"balancer"`
	Count    int    `json:"count"`
	Memory   int    `json:"memory"`
	Ports    []int  `json:"ports"`
}

type RanchFormation map[string]RanchFormationEntry

type RanchProcess struct {
	Id      string    `json:"id"`
	App     string    `json:"app"`
	Command string    `json:"command"`
	Host    string    `json:"host"`
	Image   string    `json:"image"`
	Name    string    `json:"name"`
	Ports   []string  `json:"ports"`
	Release string    `json:"release"`
	Cpu     float64   `json:"cpu"`
	Memory  float64   `json:"memory"`
	Started time.Time `json:"started"`
}

type RanchRelease struct {
	Id      string    `json:"id"`
	App     string    `json:"app"`
	Created time.Time `json:"created"`
	Status  string    `json:"status"`
}

type RanchApp struct {
	Name    string `json:"name"`
	Status  string `json:"status"`
	Release string `json:"release"`
}

var ValidAppName = regexp.MustCompile(`\A[a-z][-a-z0-9]{3,29}\z`)
var ValidProcessName = regexp.MustCompile(`\A[a-z][-a-z0-9]{2,29}\z`)
var ValidCronName = regexp.MustCompile(`\A[a-z][a-z0-9]{2,29}\z`)

func ranchUrl(pathname string) string {
	u, _ := url.Parse(viper.GetString("endpoint"))
	u.Path = path.Join(u.Path, pathname)
	return u.String()
}

func ranchClient() *gorequest.SuperAgent {
	authToken := viper.GetString("token")
	return jsonClient().
		SetBasicAuth(authToken, "x-auth-token")
}

func RanchValidateConfig(config *RanchConfig) (errors []error) {
	if !ValidAppName.MatchString(config.AppName) {
		errors = append(errors, fmt.Errorf("app name '%s' is invalid: must match %s", config.AppName, ValidAppName.String()))
	}

	if !ValidAppName.MatchString(config.ImageName) {
		errors = append(errors, fmt.Errorf("image name '%s' is invalid: must match %s", config.ImageName, ValidAppName.String()))
	}

	for name, _ := range config.Processes {
		if !ValidProcessName.MatchString(name) {
			errors = append(errors, fmt.Errorf("process name '%s' is invalid: must match %s", name, ValidProcessName.String()))
		}
		if name == "run" {
			errors = append(errors, fmt.Errorf("process name 'run' is invalid: 'run' is a reserved process name"))
		}
	}

	for name, entry := range config.Cron {
		if !ValidCronName.MatchString(name) {
			errors = append(errors, fmt.Errorf("cron name '%s' is invalid: must match %s", name, ValidCronName.String()))
		}
		tokens := strings.Fields(entry)
		if len(tokens) < 6 {
			errors = append(errors, fmt.Errorf("cron entry '%s' is invalid: must be of format '* * * * * command'", name))
		}
		if tokens[2] != "?" && tokens[4] != "?" {
			errors = append(errors, fmt.Errorf("cron entry '%s' is invalid: either day-of-week or day-of-month field must equal '?'", name))
		}
	}

	return errors
}

func RanchLoadSettings() (err error) {
	resp, body, errs := ranchClient().Get(ranchUrl("/settings")).End()

	if len(errs) > 0 {
		return errs[0]
	}

	if resp.StatusCode != 200 {
		return fmt.Errorf("unexpected HTTP response [%d]: %s", resp.StatusCode, body)
	}

	viper.SetConfigType("json")
	if err = viper.ReadConfig(bytes.NewBuffer([]byte(body))); err != nil {
		return err
	}

	return // success
}

func RanchCreateApp(appName string) (err error) {
	client := ranchClient()

	pathname := "/v1/apps"
	reqBody := fmt.Sprintf(`{"name":"%s"}`, appName)

	resp, body, errs := client.Post(ranchUrl(pathname)).Send(reqBody).End()

	if len(errs) > 0 {
		return errs[0]
	}

	makeError := func(statusCode int, message string) error {
		return fmt.Errorf("Error creating Ranch app [HTTP %d]: %s", statusCode, message)
	}

	switch resp.StatusCode {
	case 201:
		return nil
	case 400:
		var ranchError RanchApiError
		err := json.Unmarshal([]byte(body), &ranchError)
		if err == nil {
			return makeError(resp.StatusCode, ranchError.Message)
		}
	}

	return makeError(resp.StatusCode, body)
}

func RanchUpdateEnvId(ranchFile, envId string) (err error) {
	contents, err := ioutil.ReadFile(ranchFile)
	if err != nil {
		return err
	}

	re, err := regexp.Compile(`(?m)^(\s*env_id\s*:\s*)(['"\w]+)?(.*)$`)
	if err != nil {
		return err
	}

	updatedContents := re.ReplaceAll(contents, []byte("${1}"+envId+"${3}"))
	if bytes.Equal(updatedContents, contents) {
		// if we didn't find it, we'll prepend
		updatedContents = bytes.Join([][]byte{[]byte("env_id: " + envId), contents}, []byte("\n"))
	}

	err = ioutil.WriteFile(ranchFile, updatedContents, 0644)
	if err != nil {
		return err
	}

	return nil
}

func RanchGetSecret(appName, secretId string) (string, error) {

	client := ranchClient()

	pathname := fmt.Sprintf("/v1/apps/%s/secrets/%s", appName, secretId)

	resp, body, errs := client.Get(ranchUrl(pathname)).End()

	if len(errs) > 0 {
		return "", errs[0]
	} else if resp.StatusCode != 200 {
		return "", fmt.Errorf("Error fetching secret from ranch-api: status code %d", resp.StatusCode)
	}

	var secret RanchApiSecret
	if err := json.Unmarshal([]byte(body), &secret); err != nil {
		return "", err
	}

	plaintextBytes, err := base64.StdEncoding.DecodeString(secret.Content)
	if err != nil {
		return "", err
	}

	return string(plaintextBytes), nil
}

func RanchReleaseExists(appName, id string) (exists bool, err error) {
	client := ranchClient()

	url := fmt.Sprintf("/v1/apps/%s/releases/%s", appName, id)
	resp, _, errs := client.Get(ranchUrl(url)).End()

	if len(errs) > 0 {
		return false, errs[0]
	} else if resp.StatusCode == 404 {
		return false, nil
	} else if resp.StatusCode == 200 {
		return true, nil
	}

	return false, fmt.Errorf("error fetching release info: HTTP %d", resp.StatusCode)
}

func RanchCreateRelease(appName, id, convoxRelease string) error {

	client := ranchClient()

	pathname := fmt.Sprintf("/v1/apps/%s/releases", appName)
	reqBody := fmt.Sprintf(`{"id":"%s","convoxRelease":"%s"}`, id, convoxRelease)

	resp, body, errs := client.Post(ranchUrl(pathname)).Send(reqBody).End()

	if len(errs) > 0 {
		return errs[0]
	}

	makeError := func(statusCode int, message string) error {
		return fmt.Errorf("Error creating Ranch release [HTTP %d]: %s", statusCode, message)
	}

	switch resp.StatusCode {
	case 201:
		return nil
	case 400:
		var ranchError RanchApiError
		err := json.Unmarshal([]byte(body), &ranchError)
		if err == nil {
			return makeError(resp.StatusCode, ranchError.Message)
		}
	}

	return makeError(resp.StatusCode, body)
}

func RanchReleases(appName string) ([]RanchApiRelease, error) {

	client := ranchClient()

	pathname := fmt.Sprintf("/v1/apps/%s/releases", appName)

	resp, body, errs := client.Get(ranchUrl(pathname)).End()

	if len(errs) > 0 {
		return nil, errs[0]
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Error fetching releases from Ranch: status code %d", resp.StatusCode)
	}

	var ranchReleases []RanchApiRelease
	err := json.Unmarshal([]byte(body), &ranchReleases)

	if err != nil {
		return nil, err
	}

	return ranchReleases, nil
}

func RanchCreateSecret(appName, plaintext string) (secretId string, err error) {

	client := ranchClient()

	pathname := fmt.Sprintf("/v1/apps/%s/secrets", appName)

	secret := RanchApiSecret{
		Content: base64.StdEncoding.EncodeToString([]byte(plaintext)),
	}

	resp, body, errs := client.
		Post(ranchUrl(pathname)).
		Send(secret).
		End()

	if len(errs) > 0 {
		return "", errs[0]
	} else if resp.StatusCode != 201 {
		return "", fmt.Errorf("Error creating secret in ranch-api: status code %d", resp.StatusCode)
	}

	if err = json.Unmarshal([]byte(body), &secret); err != nil {
		return "", err
	}

	return secret.Id, nil
}

func RanchGetApp(appName string) (app *RanchApp, err error) {

	client := ranchClient()

	pathname := fmt.Sprintf("/v1/apps/%s", appName)

	resp, body, errs := client.
		Get(ranchUrl(pathname)).
		End()

	if len(errs) > 0 {
		return nil, errs[0]
	} else if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Error getting app from ranch-api: status code %d", resp.StatusCode)
	}

	if err = json.Unmarshal([]byte(body), &app); err != nil {
		return nil, err
	}

	return app, nil
}

func RanchGetFormation(appName string) (formation RanchFormation, err error) {

	formation = make(RanchFormation)

	client := ranchClient()

	pathname := fmt.Sprintf("/v1/apps/%s/formation", appName)

	resp, body, errs := client.
		Get(ranchUrl(pathname)).
		End()

	if len(errs) > 0 {
		return nil, errs[0]
	} else if resp.StatusCode != 200 {
		return nil, fmt.Errorf("Error getting app formation from ranch-api: status code %d", resp.StatusCode)
	}

	if err = json.Unmarshal([]byte(body), &formation); err != nil {
		return nil, err
	}

	return formation, nil
}

func RanchDeploy(appDir string, config *RanchConfig, appSha, codeSha string) (err error) {

	imageNameWithTag := strings.Join([]string{config.ImageName, appSha}, ":")

	exists, err := DockerImageExists(imageNameWithTag)
	if err != nil {
		return err
	} else if exists {
		fmt.Printf("%s docker image already exists in registry, skipping build.\n", imageNameWithTag)
	} else {
		currentSha, err := GitCurrentSha(appDir)
		if err != nil {
			return err
		}

		if appSha != currentSha {
			return fmt.Errorf("you requested a deploy of a git sha other than HEAD, but its Docker image (%s) does not already exist.  we do not yet support this -- do a full deploy instead. ", imageNameWithTag)
		}

		if err = dockerBuildAndPush(appDir, imageNameWithTag, config); err != nil {
			return err
		}
	}

	releaseId := strings.Join([]string{appSha, codeSha}, "-")

	exists, err = RanchReleaseExists(config.AppName, releaseId)
	if err != nil {
		return err
	} else if exists {
		currentRelease, err := ConvoxCurrentVersion(config.AppName)
		if err != nil {
			return err
		}

		if currentRelease != releaseId {
			fmt.Printf("promoting existing release %s\n", releaseId)
			if err = ConvoxPromote(config.AppName, releaseId); err != nil {
				return err
			}

			time.Sleep(10 * time.Second) // wait for promote to apply

			if err = ConvoxWaitForStatus(config.AppName, "running"); err != nil {
				return err
			}
		} else {
			fmt.Printf("existing release %s is currently live, skipping promote.\n", releaseId)
		}
	} else {
		buildDir, err := ioutil.TempDir("", "ranch")
		if err != nil {
			return err
		}

		fmt.Println("using build directory", buildDir)

		dockerComposeContent, err := GenerateDockerCompose(imageNameWithTag, config)
		if err != nil {
			return err
		}

		dockerCompose := path.Join(buildDir, "docker-compose.yml")

		if err = ioutil.WriteFile(dockerCompose, dockerComposeContent, 0644); err != nil {
			return err
		}

		if err = convoxDeploy(config.AppName, releaseId, buildDir); err != nil {
			return err
		}
	}

	return ConvoxScale(config.AppName, config)
}

func convoxDeploy(appName, releaseId, buildDir string) error {
	convoxReleaseId, err := ConvoxDeploy(appName, buildDir)

	if err != nil {
		return err
	}

	if err = RanchCreateRelease(appName, releaseId, convoxReleaseId); err != nil {
		return err
	}

	fmt.Printf("promoting release %s\n", releaseId)

	if err = ConvoxPromote(appName, releaseId); err != nil {
		return err
	}

	time.Sleep(10 * time.Second) // wait for promote to apply

	if err = ConvoxWaitForStatus(appName, "running"); err != nil {
		return err
	}

	return nil
}

// see https://github.com/convox/rack/pull/1044
func quoteEnvForConvox(inEnv map[string]string) map[string]string {
	outEnv := make(map[string]string)
	for k, v := range inEnv {
		outEnv[k] = strings.Replace(v, "$", "$$", -1)
	}
	return outEnv
}

func GenerateDockerCompose(imageName string, config *RanchConfig) ([]byte, error) {
	var out bytes.Buffer
	var env map[string]string

	if config.EnvId != "" {
		plaintext, err := RanchGetSecret(config.AppName, config.EnvId)
		if err != nil {
			return nil, err
		}

		env, err = ParseEnv(plaintext)
		if err != nil {
			return nil, err
		}
	}

	absoluteImageName, err := DockerResolveImageName(imageName)

	if err != nil {
		return nil, err
	}

	err = dockerComposeTemplate.Execute(&out, composeTemplateVars{
		ImageName:   absoluteImageName,
		Environment: quoteEnvForConvox(env),
		Config:      config,
	})

	if err != nil {
		return nil, err
	}

	return out.Bytes(), nil
}

func dockerBuildAndPush(appDir, imageName string, config *RanchConfig) (err error) {

	env, err := EnvGet(config.AppName, config.EnvId)

	if err != nil {
		return err
	}

	dockerfile := path.Join(appDir, "Dockerfile")

	if _, err := os.Stat(dockerfile); os.IsNotExist(err) {

		var template *template.Template

		if _, err := os.Stat(path.Join(appDir, ".buildpacks")); os.IsNotExist(err) {
			template = nodejsDockerfileTemplate
		} else {
			template = cedarDockerfileTemplate
		}

		var out bytes.Buffer
		err = template.Execute(&out, struct{}{})

		if err != nil {
			return err
		}

		err = ioutil.WriteFile(dockerfile, out.Bytes(), 0644)

		if err != nil {
			return err
		}

		defer os.Remove(dockerfile) // cleanup
	} else {
		fmt.Println("WARNING: using existing Dockerfile")
	}

	err = DockerBuild(appDir, imageName, env)

	if err != nil {
		return err
	}

	err = DockerPush(imageName)

	if err != nil {
		return err
	}

	return nil
}
