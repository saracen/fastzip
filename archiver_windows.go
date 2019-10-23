// +build windows

package fastzip

import (
	"io"

	"github.com/saracen/fastzip/internal/zip"
)

func (a *Archiver) createHeader(hdr *zip.FileHeader) (io.Writer, error) {
	return a.zw.CreateHeader(hdr)
}

func (a *Archiver) createHeaderRaw(hdr *zip.FileHeader) (io.Writer, error) {
	return a.zw.CreateHeaderRaw(hdr)
}
