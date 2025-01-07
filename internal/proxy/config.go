package proxy

import "time"

const (
	defaultTimeout               = 10 * time.Minute
	defaultPort                  = 8443
	defaultBuffer                = 1000
	targetHostHeader             = "X-Gozero-Target-Host"
	targetPortHeader             = "X-Gozero-Target-Port"
	targetSchemeHeader           = "X-Gozero-Target-Scheme"
	targetRetriesHeader          = "X-Gozero-Target-Retries"
	targetBackoffHeader          = "X-Gozero-Target-Backoff"
	defaultTargetPort            = 443
	defaultTargetScheme          = "https"
	defaultMaxRetries            = 20
	defaultInitialBackoff        = 100 * time.Millisecond
	defaultIdleTimeout           = 120 * time.Second
	defaultTLSHandshakeTimeout   = 10 * time.Second
	defaultResponseHeaderTimeout = 30 * time.Second
	defaultDialTimeout           = 300 * time.Second
)
