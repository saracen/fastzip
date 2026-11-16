package fastzip

// ExtractorOption is an option used when creating an extractor.
type ExtractorOption func(*extractorOptions) error

type extractorOptions struct {
	concurrency       int
	chownErrorHandler func(name string, err error) error
	symlinkPolicy     SymlinkPolicy
}

// SymlinkPolicy controls how symbolic link entries are handled by the
// extractor.
//
// Whatever the policy, the extractor never writes through a symbolic link:
// files, directories and links are created relative to the chroot and an
// entry whose path passes through a link that leads outside of the chroot
// fails. The policy only decides whether the extracted tree itself may end up
// containing links that point outside of the chroot.
type SymlinkPolicy int

const (
	// SymlinkPolicyPreserve creates every symbolic link as stored in the
	// archive, including links whose target is outside of the chroot. This is
	// the default, and the right choice when the archive is a faithful copy of
	// a directory tree, such as a virtual environment linking to a system
	// interpreter.
	//
	// On Windows, a link whose target is absolute or outside of the chroot is
	// created as a file link even when the target is a directory, as os.Root
	// only creates directory links for targets it can resolve inside the
	// chroot.
	SymlinkPolicyPreserve SymlinkPolicy = iota

	// SymlinkPolicyContain fails extraction with an error wrapping
	// ErrSymlinkEscapesChroot if a symbolic link's target is absolute or
	// resolves outside of the chroot, including through other links, so that
	// the extracted tree is self-contained. A dangling link whose target stays
	// inside the chroot is allowed.
	SymlinkPolicyContain
)

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

// WithExtractorSymlinkPolicy sets how symbolic link entries whose target is
// outside of the chroot are handled. The default is SymlinkPolicyPreserve.
func WithExtractorSymlinkPolicy(policy SymlinkPolicy) ExtractorOption {
	return func(o *extractorOptions) error {
		o.symlinkPolicy = policy
		return nil
	}
}
