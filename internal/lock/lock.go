package lock

type Locker interface {
	NewMutex(name string) Mutexer
}

type Mutexer interface {
	Lock() error
	Unlock() (bool, error)
	TryLock() error
}
