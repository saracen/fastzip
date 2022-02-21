//go:build windows
// +build windows

package fastzip

import (
	"io"
	"os"

	"github.com/klauspost/compress/zip"
)

func (a *Archiver) createHeader(fi os.FileInfo, hdr *zip.FileHeader) (io.Writer, error) {
	return a.zw.CreateHeader(hdr)
}

func (a *Archiver) createRaw(fi os.FileInfo, hdr *zip.FileHeader) (io.Writer, error) {
	return a.zw.CreateRaw(hdr)
}
