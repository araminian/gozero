package proxy

type Proxier interface {
	Start() error
	//Stop() error
	Requests() <-chan Requests
}

type Requests struct {
	Host string
	Path string
}
