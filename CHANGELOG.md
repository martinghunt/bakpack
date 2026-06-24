# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- Make `bakpack --version` print `bakpack X.Y.Z`, normalizing release tags like `vX.Y.Z` for display.

## [0.1.0] - 2026-06-23

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
- Read the Docs documentation with install, reduce/restore, build, extract, input source, checksum, archive format, library, and release pages.

### Notes

- Restored JSON is verified by canonical SHA-256. Object key order and whitespace are not preserved.
- Archive format compatibility is not guaranteed before a future stable release.

[Unreleased]: https://github.com/martinghunt/bakpack/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/martinghunt/bakpack/releases/tag/v0.1.0
