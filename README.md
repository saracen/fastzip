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
if err = a.Archive(context.Background(), files); err != nil {
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
if err = e.Extract(context.Background()); err != nil {
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
BenchmarkArchiveStore_1-24                            39         788604969 ns/op         421.66 MB/s     9395405 B/op     266271 allocs/op
BenchmarkArchiveStandardFlate_1-24                     2        16154127468 ns/op         20.58 MB/s    12075824 B/op     257251 allocs/op
BenchmarkArchiveStandardFlate_2-24                     4        8686391074 ns/op          38.28 MB/s    15898644 B/op     260757 allocs/op
BenchmarkArchiveStandardFlate_4-24                     7        4391603068 ns/op          75.72 MB/s    19295604 B/op     260871 allocs/op
BenchmarkArchiveStandardFlate_8-24                    14        2291624196 ns/op         145.10 MB/s    21999205 B/op     260970 allocs/op
BenchmarkArchiveStandardFlate_16-24                   16        2105056696 ns/op         157.96 MB/s    29237232 B/op     261225 allocs/op
BenchmarkArchiveNonStandardFlate_1-24                  6        6011250439 ns/op          55.32 MB/s    11070960 B/op     257204 allocs/op
BenchmarkArchiveNonStandardFlate_2-24                  9        3629347294 ns/op          91.62 MB/s    18870130 B/op     262279 allocs/op
BenchmarkArchiveNonStandardFlate_4-24                 18        1766182097 ns/op         188.27 MB/s    22976928 B/op     262349 allocs/op
BenchmarkArchiveNonStandardFlate_8-24                 34        1002516188 ns/op         331.69 MB/s    29860872 B/op     262473 allocs/op
BenchmarkArchiveNonStandardFlate_16-24                46         757112363 ns/op         439.20 MB/s    42036132 B/op     262714 allocs/op
BenchmarkExtractStore_1-24                            20        1625582744 ns/op         202.66 MB/s    22900375 B/op     330528 allocs/op
BenchmarkExtractStore_2-24                            42         786644031 ns/op         418.80 MB/s    22307976 B/op     329272 allocs/op
BenchmarkExtractStore_4-24                            92         384075767 ns/op         857.76 MB/s    22247288 B/op     328667 allocs/op
BenchmarkExtractStore_8-24                           165         215884636 ns/op        1526.02 MB/s    22354996 B/op     328459 allocs/op
BenchmarkExtractStore_16-24                          226         157087517 ns/op        2097.20 MB/s    22258691 B/op     328393 allocs/op
BenchmarkExtractStandardFlate_1-24                     6        5501808448 ns/op          23.47 MB/s    86148462 B/op     495586 allocs/op
BenchmarkExtractStandardFlate_2-24                    13        2748387174 ns/op          46.99 MB/s    84232141 B/op     491343 allocs/op
BenchmarkExtractStandardFlate_4-24                    21        1511063035 ns/op          85.47 MB/s    84998750 B/op     490124 allocs/op
BenchmarkExtractStandardFlate_8-24                    32         995911009 ns/op         129.67 MB/s    86188957 B/op     489574 allocs/op
BenchmarkExtractStandardFlate_16-24                   46         652641882 ns/op         197.88 MB/s    88256113 B/op     489575 allocs/op
BenchmarkExtractNonStandardFlate_1-24                  7        4989810851 ns/op          25.88 MB/s    64552948 B/op     373541 allocs/op
BenchmarkExtractNonStandardFlate_2-24                 13        2478287953 ns/op          52.11 MB/s    63413947 B/op     373183 allocs/op
BenchmarkExtractNonStandardFlate_4-24                 26        1333552250 ns/op          96.84 MB/s    63546389 B/op     373925 allocs/op
BenchmarkExtractNonStandardFlate_8-24                 37         817039739 ns/op         158.06 MB/s    64354655 B/op     375357 allocs/op
BenchmarkExtractNonStandardFlate_16-24                63         566984549 ns/op         227.77 MB/s    65444227 B/op     379664 allocs/op
```
