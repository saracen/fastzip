# fastzip

[![godoc](https://godoc.org/github.com/saracen/fastzip?status.svg)](http://godoc.org/github.com/saracen/fastzip)
[![Build Status](https://travis-ci.org/saracen/fastzip.svg?branch=master)](https://travis-ci.org/saracen/fastzip)

Fastzip is an opinionated Zip archiver and extractor with a focus on speed.

- Archiving and extraction of files and directories can only occur within
  a specified directory.
- Permissions, ownership (uid, gid on linux/unix) and modification times are
  preserved.
- Buffers used for copying files are recycled to reduce allocations.
- Files are archived and extracted concurrently.
- By default, the excellent
  [`github.com/klauspost/compress/flate`](https://github.com/klauspost/compress)
  library is used for compression and decompression.

## Example
### Archiver
```go
// Create archive file
w, err := os.Create("archive.zip")
if err != nil {
  panic(err)
}
defer w.Close()

// Create new Archiver
a, err := fastzip.NewArchiver(w, "~/fastzip-archiving")
if err != nil {
  panic(err)
}
defer a.Close()

// Register a non-default level compressor if required
// a.RegisterCompressor(zip.Deflate, fastzip.FlateCompressor(1))

// Walk directory, adding the files we want to add
files := make(map[string]os.FileInfo)
err = filepath.Walk("~/fastzip-archiving", func(pathname string, info os.FileInfo, err error) error {
	files[pathname] = info
	return nil
})

// Archive
if err = a.Archive(files); err != nil {
  panic(err)
}
```

### Extractor
```go
// Create new extractor
e, err := fastzip.NewExtractor("archive.zip", "~/fastzip-extraction")
if err != nil {
  panic(err)
}
defer e.Close()

// Extract archive files
if err = e.Extract(); err != nil {
  panic(err)
}
```

## Benchmarks

Archiving and extracting a Go 1.13 GOROOT directory, 342M, 10308 files.

StandardFlate is using  `compress/flate`, NonStandardFlate is
`klauspost/compress/flate`, both on level 5. This was performed on a server with an SSD and 24-cores. Each test was conducted
using the `WithArchiverConcurrency` and `WithExtractorConcurrency` options of 1, 2, 4, 8 and 16.

```
$ go test -bench Benchmark* -archivedir go1.13 -benchtime=30s -timeout=20m

goos: linux
goarch: amd64
pkg: github.com/saracen/fastzip
BenchmarkArchiveStore_1-24                            57         759214314 ns/op         437.98 MB/s     9402776 B/op     266273 allocs/op
BenchmarkArchiveStandardFlate_1-24                     2        16635779058 ns/op         19.99 MB/s    11278584 B/op     257235 allocs/op
BenchmarkArchiveStandardFlate_2-24                     4        8958114992 ns/op          37.12 MB/s    15874110 B/op     260757 allocs/op
BenchmarkArchiveStandardFlate_4-24                     7        4513852744 ns/op          73.67 MB/s    18144582 B/op     260842 allocs/op
BenchmarkArchiveStandardFlate_8-24                    14        2337987195 ns/op         142.23 MB/s    22054485 B/op     260969 allocs/op
BenchmarkArchiveStandardFlate_16-24                   15        2112499873 ns/op         157.41 MB/s    27918230 B/op     261194 allocs/op
BenchmarkArchiveNonStandardFlate_1-24                  5        6313696025 ns/op          52.67 MB/s    15156160 B/op     257217 allocs/op
BenchmarkArchiveNonStandardFlate_2-24                  9        3741371342 ns/op          88.88 MB/s    30771747 B/op     261645 allocs/op
BenchmarkArchiveNonStandardFlate_4-24                 18        1906735146 ns/op         174.39 MB/s    35048840 B/op     261690 allocs/op
BenchmarkArchiveNonStandardFlate_8-24                 33        1046698073 ns/op         317.69 MB/s    49022688 B/op     261807 allocs/op
BenchmarkArchiveNonStandardFlate_16-24                36         889191472 ns/op         373.96 MB/s    75247073 B/op     262044 allocs/op
BenchmarkExtractStore_1-24                            24        1491304034 ns/op         220.91 MB/s    22625127 B/op     330125 allocs/op
BenchmarkExtractStore_2-24                            43         717933301 ns/op         458.88 MB/s    22280720 B/op     329243 allocs/op
BenchmarkExtractStore_4-24                            98         362879118 ns/op         907.86 MB/s    22231260 B/op     328635 allocs/op
BenchmarkExtractStore_8-24                           174         207128645 ns/op        1590.53 MB/s    22369458 B/op     328445 allocs/op
BenchmarkExtractStore_16-24                          232         153516463 ns/op        2145.99 MB/s    22326276 B/op     328391 allocs/op
BenchmarkExtractStandardFlate_1-24                     6        5134276149 ns/op          25.20 MB/s    92484254 B/op     525571 allocs/op
BenchmarkExtractStandardFlate_2-24                    12        2735566653 ns/op          47.29 MB/s    91496545 B/op     521703 allocs/op
BenchmarkExtractStandardFlate_4-24                    25        1413308343 ns/op          91.53 MB/s    91636712 B/op     519786 allocs/op
BenchmarkExtractStandardFlate_8-24                    38         917288285 ns/op         141.02 MB/s    93474913 B/op     519425 allocs/op
BenchmarkExtractStandardFlate_16-24                   48         628763448 ns/op         205.74 MB/s    95599041 B/op     519594 allocs/op
BenchmarkExtractNonStandardFlate_1-24                  6        5173029578 ns/op          25.01 MB/s    64130228 B/op     352198 allocs/op
BenchmarkExtractNonStandardFlate_2-24                 13        2685023282 ns/op          48.18 MB/s    63327702 B/op     353975 allocs/op
BenchmarkExtractNonStandardFlate_4-24                 25        1460385105 ns/op          88.58 MB/s    63177508 B/op     354165 allocs/op
BenchmarkExtractNonStandardFlate_8-24                 36         901423814 ns/op         143.51 MB/s    64567442 B/op     357130 allocs/op
BenchmarkExtractNonStandardFlate_16-24                57         635525487 ns/op         203.55 MB/s    65348558 B/op     360551 allocs/op

```
