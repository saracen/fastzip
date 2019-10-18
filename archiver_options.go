package fastzip

// ArchiverOption is an option used when creating an archiver.
type ArchiverOption func(*archiverOptions) error

type archiverOptions struct {
	method uint16
}

func WithArchiverMethod(method uint16) ArchiverOption {
	return func(o *archiverOptions) error {
		o.method = method
		return nil
	}
}
