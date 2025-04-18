package config

import (
	"os"
	"strconv"
	"time"

	"github.com/sirupsen/logrus"
)

func GetEnvOrDefaultString(key, defaultValue string) string {
	value := os.Getenv(key)
	if len(value) == 0 {
		return defaultValue
	}
	return value
}

func GetEnvOrDefaultInt(key string, defaultValue int) int {
	value := os.Getenv(key)
	if len(value) == 0 {
		return defaultValue
	}
	intValue, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}
	return intValue
}

func GetEnvOrDefaultDuration(key string, defaultValue time.Duration) time.Duration {
	value := os.Getenv(key)
	if len(value) == 0 {
		return defaultValue
	}
	intValue, err := strconv.Atoi(value)
	if err != nil {
		return defaultValue
	}
	return time.Duration(intValue) * time.Second
}

func GetEnvOrDefaultLogLevel(key, defaultValue string) logrus.Level {
	value := GetEnvOrDefaultString(key, defaultValue)
	level, err := logrus.ParseLevel(value)
	if err != nil {
		return logrus.InfoLevel
	}
	return level
}
