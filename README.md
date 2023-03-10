# rss2masto

[![Go Report Card](https://goreportcard.com/badge/github.com/glaydus/rss2masto)](https://goreportcard.com/report/github.com/glaydus/rss2masto)
[![Release](https://img.shields.io/github/v/release/glaydus/rss2masto)](https://github.com/glaydus/rss2masto/releases/latest)
[![Go Reference](https://pkg.go.dev/badge/github.com/glaydus/rss2masto.svg)](https://pkg.go.dev/github.com/glaydus/rss2masto)
![GitHub go.mod Go version](https://img.shields.io/github/go-mod/go-version/glaydus/rss2masto)
[![License](https://img.shields.io/:license-mit-blue.svg)](http://doge.mit-license.org)

Publish a RSS feed to Mastodon status

## Installation

### Go modules

If your application uses Go modules for dependency management (recommended), add an import for each service that you use in your application.

Example:

```go
import (
  "github.com/glaydus/rss2masto"
)
```

Next, run `go build` or `go mod tidy` to download and install the new dependencies and update your application's `go.mod` file.

### `go get` command

Alternatively, you can use the `go get` command to download and install the appropriate packages that your application uses:

```sh
go get -u github.com/glaydus/rss2masto
```

## Code example

```go
package main

import (
	"log"

	"github.com/glaydus/rss2masto"
)

func main() {
	fm, err := rss2masto.NewFeedsMonitor()
	if err != nil {
		log.Fatalln(err)
	}
	fm.Start()
}
```
