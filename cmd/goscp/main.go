package main

import (
	"os"

	"github.com/sirupsen/logrus"
)

func main() {
	logger := logrus.New()
	logger.SetOutput(os.Stdout)

	if err := Execute(logger); err != nil {
		logger.Fatal(err)
	}
}