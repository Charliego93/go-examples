package main

import (
	"github.com/AlecAivazis/survey/v2"
	"github.com/whimthen/kits/logger"
	"os"
	"os/exec"
)

func CompleteNginx() {
	path, err := checkNginxInstall()
	if err != nil {
		isInstall := false
		prompt := &survey.Confirm{
			Message: "",
		}
		err := survey.AskOne(prompt, &isInstall)
		if err != nil {

		}
	}
	cmd := exec.Command("bash", "-c", "sudo " + path)
	cmd.Stderr = os.Stderr
	cmd.Stdout = os.Stdout
	cmd.Stdin = os.Stdin
	err = cmd.Run()

	//_ = cmd.Wait()

	if err != nil {
		logger.Error("%s", err)
		return
	}
}

func checkNginxInstall() (path string, err error) {
	return checkInstall("nginx")
}

func checkBrewInstall() (path string, err error) {
	return checkInstall("brew")
}

func checkInstall(file string) (path string, err error) {
	path, err = exec.LookPath(file)
	if err != nil {
		return "", err
	}
	return path, nil
}