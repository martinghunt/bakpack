# Install

## Download

Download the latest build from the
[latest release](https://github.com/martinghunt/bakpack/releases/latest).

Choose the archive or binary that matches your operating system and CPU
architecture. Put the `bakpack` executable somewhere on your `PATH`, then check:

```
bakpack --version
```

Release pages include a SHA-256 checksum file named like
`bakpack-v0.1.0-checksums.txt`.

## External programs

Archive creation uses the command line `xz` program:

```
xz -9e -T1 -c
```

Put `xz` on `PATH` before building archives.

AGC genome input requires `agc` on `PATH`.

## macOS

If macOS Gatekeeper blocks the downloaded binary, allow it in "Privacy &
Security" in the Settings app, or remove the quarantine attribute in a terminal:

```
xattr -d com.apple.quarantine /path/to/bakpack
```

## Windows

Download the Windows build, extract it if needed, and run `bakpack.exe` from a
terminal. If Windows Defender warns about the binary, allow it if you trust the
downloaded release.

## Linux

You may need to make the downloaded file executable:

```
chmod +x bakpack
```

Then move it to a directory on your `PATH`.

## Build locally

Local builds require Go.

```
./build.sh
```

That builds `bakpack` for the current OS and architecture into
`./build/bakpack` or `./build/bakpack.exe`. Local builds report version `dev`
unless you pass an explicit release version.

For a cross-platform release build:

```
./build.sh --release --version v1.2.3
```
