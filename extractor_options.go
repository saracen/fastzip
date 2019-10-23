package fastzip

// ExtractorOption is an option used when creating an extractor.
type ExtractorOption func(*extractorOptions) error

type extractorOptions struct {
	concurrency int
}

// WithExtractorConcurrency will set the maximum number of files being
// extracted concurrently. The default is GOMAXPROCS.
func WithExtractorConcurrency(n int) ExtractorOption {
	return func(o *extractorOptions) error {
		if n <= 0 {
			return ErrMinConcurrency
		}
		o.concurrency = n
		return nil
	}
}
