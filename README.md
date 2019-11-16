# fastzip

Fastzip is an opinionated Zip archiver and extractor with a focus on speed.

- Archiving and extraction of files and directories can only occur within
  a specified directory.
- Permissions, ownership (uid, gid on linux/unix) and modification times are
  preserved.
- Buffers used for copying files are recycled to reduce allocations.
- Files are archived and extracted concurrently.
- By default, the standard library's `compress/flate` is used, however, you can
  easily register this library's preferred deflate encoder with:
    - `archive.RegisterCompressor(zip.Deflate, fastzip.FlateCompressor(5))`
    - `extractor.RegisterDecompressor(zip.Deflate, fastzip.FlateDecompressor())`
  
  This uses the excellent [`github.com/klauspost/compress/flate`](https://github.com/klauspost/compress)
  library and is 30-40% faster than the standard library.

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

// Register faster compressor, level 5 compression
a.RegisterCompressor(zip.Deflate, fastzip.FlateCompressor(5))

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

// Register faster decompressor
e.RegisterDecompressor(zip.Deflate, fastzip.FlateDecompressor())

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
goos: linux
goarch: amd64
pkg: github.com/saracen/fastzip
BenchmarkArchiveStore_1-24                             2         827436658 ns/op         401.87 MB/s     9432392 B/op     266281 allocs/op
BenchmarkArchiveStandardFlate_1-24                     1        13345510211 ns/op         24.92 MB/s    11723112 B/op     257254 allocs/op
BenchmarkArchiveStandardFlate_2-24                     1        7158552137 ns/op          46.45 MB/s    15866408 B/op     260805 allocs/op
BenchmarkArchiveStandardFlate_4-24                     1        3748912799 ns/op          88.70 MB/s    21701080 B/op     260984 allocs/op
BenchmarkArchiveStandardFlate_8-24                     1        1929447410 ns/op         172.34 MB/s    24345232 B/op     261216 allocs/op
BenchmarkArchiveStandardFlate_16-24                    1        1601845052 ns/op         207.59 MB/s    27819232 B/op     261527 allocs/op
BenchmarkArchiveNonStandardFlate_1-24                  1        6323870186 ns/op          52.58 MB/s    15162312 B/op     257225 allocs/op
BenchmarkArchiveNonStandardFlate_2-24                  1        3974602831 ns/op          83.66 MB/s    39907560 B/op     261764 allocs/op
BenchmarkArchiveNonStandardFlate_4-24                  1        2011674444 ns/op         165.30 MB/s    46077496 B/op     261823 allocs/op
BenchmarkArchiveNonStandardFlate_8-24                  1        1141812959 ns/op         291.22 MB/s    64150920 B/op     261970 allocs/op
BenchmarkArchiveNonStandardFlate_16-24                 2         900114760 ns/op         369.42 MB/s    83224376 B/op     262219 allocs/op
BenchmarkExtractStore_1-24                             1        1533926053 ns/op         214.77 MB/s    52866536 B/op     376590 allocs/op
BenchmarkExtractStore_2-24                             2         765725804 ns/op         430.24 MB/s    37453780 B/op     352368 allocs/op
BenchmarkExtractStore_4-24                             3         400612015 ns/op         822.35 MB/s    32543053 B/op     344306 allocs/op
BenchmarkExtractStore_8-24                             5         244330423 ns/op        1348.35 MB/s    28740785 B/op     337885 allocs/op
BenchmarkExtractStore_16-24                            6         176670429 ns/op        1864.74 MB/s    27412849 B/op     336255 allocs/op
BenchmarkExtractStandardFlate_1-24                     1        5555366310 ns/op          23.29 MB/s    116613120 B/op    574230 allocs/op
BenchmarkExtractStandardFlate_2-24                     1        2891878658 ns/op          44.73 MB/s    117078464 B/op    574266 allocs/op
BenchmarkExtractStandardFlate_4-24                     1        1437946901 ns/op          89.96 MB/s    117199504 B/op    574274 allocs/op
BenchmarkExtractStandardFlate_8-24                     1        1070725688 ns/op         120.81 MB/s    117706240 B/op    574328 allocs/op
BenchmarkExtractStandardFlate_16-24                    2         730608470 ns/op         177.06 MB/s    105287080 B/op    550492 allocs/op
BenchmarkExtractNonStandardFlate_1-24                  1        4797270832 ns/op          26.97 MB/s    89298080 B/op     390746 allocs/op
BenchmarkExtractNonStandardFlate_2-24                  1        2670061801 ns/op          48.45 MB/s    89585600 B/op     392432 allocs/op
BenchmarkExtractNonStandardFlate_4-24                  1        1461367611 ns/op          88.52 MB/s    89899552 B/op     394237 allocs/op
BenchmarkExtractNonStandardFlate_8-24                  2         839255530 ns/op         154.14 MB/s    77181004 B/op     374984 allocs/op
BenchmarkExtractNonStandardFlate_16-24                 2         626829180 ns/op         206.37 MB/s    76094588 B/op     374289 allocs/op
```
