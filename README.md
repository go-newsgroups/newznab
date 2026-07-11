<p align="center"><img src="https://raw.githubusercontent.com/go-newsgroups/brand/main/social/go-newsgroups.png" alt="go-newsgroups/newznab" width="720"></p>

# newznab

[![CI](https://github.com/go-newsgroups/newznab/actions/workflows/ci.yml/badge.svg)](https://github.com/go-newsgroups/newznab/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/go-newsgroups/newznab.svg)](https://pkg.go.dev/github.com/go-newsgroups/newznab)
[![License: BSD-3-Clause](https://img.shields.io/badge/License-BSD--3--Clause-blue.svg)](LICENSE)

A pure-Go, dependency-free client for the [Newznab](https://newznab.readthedocs.io/)
Usenet indexer API. It works both against direct Newznab indexers (nzbgeek,
NZBFinder, DrunkenSlug, …) and against [NZBHydra2](https://github.com/theotherp/nzbhydra2),
which exposes a Newznab-superset API.

- **CGO-free** (`CGO_ENABLED=0`) and **zero third-party dependencies** — standard library only.
- Newznab `t=search` and `t=caps` endpoints.
- Network-free tests, base URL overridable for full testability.

## Install

```sh
go get github.com/go-newsgroups/newznab
```

## Usage

```go
package main

import (
	"context"
	"fmt"
	"log"

	"github.com/go-newsgroups/newznab"
)

func main() {
	// Works with a direct indexer or an NZBHydra2 instance — pass the root URL.
	c := newznab.New("https://api.nzbgeek.info", "YOUR_API_KEY")

	res, err := c.Search(context.Background(), newznab.SearchOptions{
		Query:      "ubuntu 24.04",
		Categories: []int{4000}, // e.g. PC
		Limit:      50,
	})
	if err != nil {
		log.Fatal(err)
	}

	fmt.Printf("%d of %d results\n", len(res.Items), res.Total)
	for _, it := range res.Items {
		fmt.Printf("%s (%d bytes)\n  %s\n", it.Title, it.Size, it.NZBURL)
	}

	// Discover available categories.
	cats, err := c.Capabilities(context.Background())
	if err != nil {
		log.Fatal(err)
	}
	for _, cat := range cats {
		fmt.Printf("%s: %s\n", cat.ID, cat.Name)
	}
}
```

## License

BSD-3-Clause. See [LICENSE](LICENSE).
