package proxy

type Proxier interface {
	Start() error
	//Stop() error
}
