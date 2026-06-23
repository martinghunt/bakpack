# Release process

Releases are made from Git tags. The GitHub Actions release workflow runs when
a tag matching `v*.*.*` is pushed. It runs the tests, builds binaries for
Darwin, Linux, and Windows on amd64 and arm64, then uploads the archives to the
GitHub release.

Before tagging, run:

```
go test ./...
./build.sh
```

Build the documentation locally with:

```
python3 -m pip install -r docs/requirements.txt
python3 -m sphinx -b html docs docs/_build/html
```

Then open `docs/_build/html/index.html` in a browser. For live rebuilds while
editing docs, run:

```
python3 -m sphinx_autobuild docs docs/_build/html
```

Then create and push the release tag:

```
git tag -a v1.2.3 -m "bakpack v1.2.3"
git push origin main
git push origin v1.2.3
```

For a local check of the full release matrix:

```
./build.sh --release --version v1.2.3
```
