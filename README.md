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
```
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
```
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
BenchmarkArchiveStore_1-24                             2         767790415 ns/op         7546972 B/op     164417 allocs/op
BenchmarkArchiveStandardFlate_1-24                     1        12701854697 ns/op        9031584 B/op     155352 allocs/op
BenchmarkArchiveStandardFlate_2-24                     1        7231142160 ns/op        14654976 B/op     158651 allocs/op
BenchmarkArchiveStandardFlate_4-24                     1        3529653713 ns/op        19643472 B/op     158806 allocs/op
BenchmarkArchiveStandardFlate_8-24                     1        2065915120 ns/op        20600568 B/op     158941 allocs/op
BenchmarkArchiveStandardFlate_16-24                    1        1585204206 ns/op        35697976 B/op     159616 allocs/op
BenchmarkArchiveNonStandardFlate_1-24                  1        6639106839 ns/op        10377136 B/op     155331 allocs/op
BenchmarkArchiveNonStandardFlate_2-24                  1        3882063711 ns/op        29038464 B/op     159414 allocs/op
BenchmarkArchiveNonStandardFlate_4-24                  1        2064839564 ns/op        32078616 B/op     159452 allocs/op
BenchmarkArchiveNonStandardFlate_8-24                  1        1077984082 ns/op        68054488 B/op     159777 allocs/op
BenchmarkArchiveNonStandardFlate_16-24                 2         902048164 ns/op        69093092 B/op     159835 allocs/op
BenchmarkExtractStore_1-24                             1        4937620997 ns/op        114333728 B/op    513607 allocs/op
BenchmarkExtractStore_2-24                             1        2760969294 ns/op        114625376 B/op    513614 allocs/op
BenchmarkExtractStore_4-24                             1        1370455758 ns/op        114699696 B/op    513628 allocs/op
BenchmarkExtractStore_8-24                             2         931258382 ns/op        101096036 B/op    489593 allocs/op
BenchmarkExtractStore_16-24                            2         769617330 ns/op        103773148 B/op    489937 allocs/op
BenchmarkExtractStandardFlate_1-24                     1        5392231233 ns/op        114553664 B/op    513624 allocs/op
BenchmarkExtractStandardFlate_2-24                     1        2880754249 ns/op        114626128 B/op    513622 allocs/op
BenchmarkExtractStandardFlate_4-24                     1        1469073062 ns/op        115026176 B/op    513661 allocs/op
BenchmarkExtractStandardFlate_8-24                     2         876169089 ns/op        102183440 B/op    489708 allocs/op
BenchmarkExtractStandardFlate_16-24                    2         676284180 ns/op        104806528 B/op    490060 allocs/op
BenchmarkExtractNonStandardFlate_1-24                  1        5260825440 ns/op        87035856 B/op     329816 allocs/op
BenchmarkExtractNonStandardFlate_2-24                  1        2493055665 ns/op        87566864 B/op     332024 allocs/op
BenchmarkExtractNonStandardFlate_4-24                  1        1608618424 ns/op        87261736 B/op     332567 allocs/op
BenchmarkExtractNonStandardFlate_8-24                  2         923286287 ns/op        72071512 B/op     309891 allocs/op
BenchmarkExtractNonStandardFlate_16-24                 2         642293998 ns/op        74520496 B/op     315397 allocs/op
```