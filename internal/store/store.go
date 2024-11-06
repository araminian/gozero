package store

import "time"

type Storer interface {
	Ping() (bool, error)
	Close() error
	GetAllScaleUpKeys() ([]string, error)
	ScaleUp(host string, scaleThreshold int, scaleDuration time.Duration) error
	ScaleDown(host string) error
	ResetTimer(host string, scaleDuration time.Duration) error
	GetAllScaleUpKeysValues() (map[string]string, error)
}
