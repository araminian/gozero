package metric

type Storer interface {
	GetAllScaleUpKeysValues() (map[string]string, error)
}
