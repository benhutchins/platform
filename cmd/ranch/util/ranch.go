package util

import (
	"bytes"
	"io/ioutil"
	"path"
	"regexp"
	"time"
)

type RanchConfig struct {
	Name      string                `json:"name"`
	EnvId     string                `json:"env_id"`
	Processes RanchConfigProcessMap `json:"processes"`
}

type RanchConfigProcess struct {
	Command   string `json:"command"`
	Instances int    `json:"instances"`
	Memory    int    `json:"memory"`
}

type RanchConfigProcessMap map[string]RanchConfigProcess

type Process struct {
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

type Processes []Process

type Release struct {
	Id      string    `json:"id"`
	App     string    `json:"app"`
	Created time.Time `json:"created"`
	Status  string    `json:"status"`
}

type Releases []Release

func RanchUpdateEnvId(appDir, envId string) (err error) {
	ranchFile := path.Join(appDir, ".ranch.yaml")

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
