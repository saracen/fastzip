package fastzip

// ExtractorOption is an option used when creating an extractor.
type ExtractorOption func(*extractorOptions) error

type extractorOptions struct {
	concurrency       int
	chownErrorHandler func(name string, err error) error
}

// WithExtractorConcurrency will set the maximum number of files being
// extracted concurrently. The default is set to GOMAXPROCS.
func WithExtractorConcurrency(n int) ExtractorOption {
	return func(o *extractorOptions) error {
		if n <= 0 {
			return ErrMinConcurrency
		}
		o.concurrency = n
		return nil
	}
}

// WithExtractorChownErrorHandler sets an error handler to be called if errors are
// encountered when trying to preserve ownership of extracted files. Returning
// nil will continue extraction, returning any error will cause Extract() to
// error.
func WithExtractorChownErrorHandler(fn func(name string, err error) error) ExtractorOption {
	return func(o *extractorOptions) error {
		o.chownErrorHandler = fn
		return nil
	}
}
