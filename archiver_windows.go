// +build windows

package fastzip

import (
	"archive/zip"
	"io"
)

func (a *Archiver) createHeader(hdr *zip.FileHeader) (io.Writer, error) {
	return a.zw.CreateHeader(hdr)
}
