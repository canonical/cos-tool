package main

import (
	cmd "github.com/canonical/cos-tool/cmd/root"
	log "github.com/sirupsen/logrus"
)

func main() {
	err := cmd.Execute()
	if err != nil {
		log.Fatal(err)
	}
}
