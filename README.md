# fastzip

Fastzip is an opinionated Zip archiver and extractor with a focus on speed.

- Archiving and extraction of files and directories can only occur within
  a specified directory.
- Permissions, ownership (uid, gid on linux/unix) and modification times are
  preserved.
- Buffers used for copying files are recycled to reduce allocations.
- Files are extracted concurrently.
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