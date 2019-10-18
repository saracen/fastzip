package fastzip

import "fmt"

// ExtractorOption is an option used when creating an extractor.
type ExtractorOption func(*extractorOptions) error

type extractorOptions struct {
	concurrency int
}

// WithExtractionConcurrency will set the maximum number of files being
// extracted concurrently. The default is GOMAXPROCS.
func WithExtractionConcurrency(n int) ExtractorOption {
	return func(o *extractorOptions) error {
		if n <= 0 {
			return fmt.Errorf("concurrency must be at least 1")
		}
		o.concurrency = n
		return nil
	}
}
