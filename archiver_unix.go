//go:build !windows
// +build !windows

package fastzip

import (
	"io"
	"math/big"
	"os"
	"syscall"

	"github.com/klauspost/compress/zip"
	"github.com/saracen/zipextra"
)

func (a *Archiver) createHeader(fi os.FileInfo, hdr *zip.FileHeader) (io.Writer, error) {
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if ok {
		hdr.Extra = append(hdr.Extra, zipextra.NewInfoZIPNewUnix(big.NewInt(int64(stat.Uid)), big.NewInt(int64(stat.Gid))).Encode()...)
	}

	return a.zw.CreateHeader(hdr)
}

func (a *Archiver) createRaw(fi os.FileInfo, hdr *zip.FileHeader) (io.Writer, error) {
	stat, ok := fi.Sys().(*syscall.Stat_t)
	if ok {
		hdr.Extra = append(hdr.Extra, zipextra.NewInfoZIPNewUnix(big.NewInt(int64(stat.Uid)), big.NewInt(int64(stat.Gid))).Encode()...)
	}

	return a.zw.CreateRaw(hdr)
}
