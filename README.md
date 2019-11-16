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
BenchmarkArchiveStore_1-24                             2         772482855 ns/op         8962844 B/op     257183 allocs/op
BenchmarkArchiveStandardFlate_1-24                     1        13196699393 ns/op        9586992 B/op     248114 allocs/op
BenchmarkArchiveStandardFlate_2-24                     1        7300203038 ns/op        15313704 B/op     251330 allocs/op
BenchmarkArchiveStandardFlate_4-24                     1        3539821258 ns/op        24525648 B/op     251646 allocs/op
BenchmarkArchiveStandardFlate_8-24                     1        1944286406 ns/op        19624032 B/op     251541 allocs/op
BenchmarkArchiveStandardFlate_16-24                    1        1790596744 ns/op        29944856 B/op     252091 allocs/op
BenchmarkArchiveNonStandardFlate_1-24                  1        6247745899 ns/op        14758776 B/op     248133 allocs/op
BenchmarkArchiveNonStandardFlate_2-24                  1        3764029513 ns/op        33551632 B/op     252126 allocs/op
BenchmarkArchiveNonStandardFlate_4-24                  1        1854558293 ns/op        27617888 B/op     252174 allocs/op
BenchmarkArchiveNonStandardFlate_8-24                  1        1128922279 ns/op        75541672 B/op     252550 allocs/op
BenchmarkArchiveNonStandardFlate_16-24                 2         945668228 ns/op        90173168 B/op     252723 allocs/op
BenchmarkExtractStore_1-24                             1        5364033610 ns/op        116324288 B/op    565157 allocs/op
BenchmarkExtractStore_2-24                             1        2639053970 ns/op        116255864 B/op    565142 allocs/op
BenchmarkExtractStore_4-24                             1        1708197512 ns/op        116609776 B/op    565178 allocs/op
BenchmarkExtractStore_8-24                             2         874622744 ns/op        103196552 B/op    541182 allocs/op
BenchmarkExtractStore_16-24                            2         687568570 ns/op        106350048 B/op    541573 allocs/op
BenchmarkExtractStandardFlate_1-24                     1        5142607436 ns/op        116689128 B/op    565209 allocs/op
BenchmarkExtractStandardFlate_2-24                     1        2737468656 ns/op        116357072 B/op    565158 allocs/op
BenchmarkExtractStandardFlate_4-24                     1        1351563778 ns/op        116649904 B/op    565178 allocs/op
BenchmarkExtractStandardFlate_8-24                     2         996574326 ns/op        103007976 B/op    541164 allocs/op
BenchmarkExtractStandardFlate_16-24                    2         658321262 ns/op        105358128 B/op    541463 allocs/op
BenchmarkExtractNonStandardFlate_1-24                  1        5076043375 ns/op        88775520 B/op     381157 allocs/op
BenchmarkExtractNonStandardFlate_2-24                  1        2714489120 ns/op        89336000 B/op     384490 allocs/op
BenchmarkExtractNonStandardFlate_4-24                  1        1702744403 ns/op        89313920 B/op     384366 allocs/op
BenchmarkExtractNonStandardFlate_8-24                  2         976734059 ns/op        74924428 B/op     362664 allocs/op
BenchmarkExtractNonStandardFlate_16-24                 2         662083060 ns/op        75818612 B/op     365682 allocs/op

```
