package config

import "os"

func Version() string {
	version := os.Getenv("VERSION")

	if version == "" {
		version = "latest"
	}

	return version
}

func GitCommit() string {
	gitCommit := os.Getenv("GIT_COMMIT")

	if gitCommit == "" {
		gitCommit = "main"
	}

	return gitCommit
}
