# Changelog

## v0.1.0

Initial release of `bakpack`.

### Added

- Reduce Bakta JSON by removing genome-derived fields while embedding canonical checksums.
- Restore original Bakta JSON content from reduced JSON and matching genome FASTA.
- Build `.bakpack` archives from directories, file lists, `.tar.xz` archives, AGC genome archives, or combined manifests.
- Extract reduced annotations, reconstructed original annotations, and genome FASTA for one or more samples.
- HTTP(S) byte-range extraction for `.bakpack` archives hosted on range-capable servers.
- Chunk-local specialized columnar archive codec with xz-compressed index and chunks.
- CLI and library APIs.
- Release build script and GitHub Actions release workflow.

### Notes

- Restored JSON is verified by canonical SHA-256. Object key order and whitespace are not preserved.
- Archive format compatibility is not guaranteed before a future stable release.
