// This module declaration exists solely to wall frontend/ off from the root
// Go module's package discovery. node_modules can ship arbitrary .go files
// (e.g. eslint's file-entry-cache -> flat-cache -> flatted bundles one), and
// without a module boundary here, `go build/vet/test ./...`, golangci-lint,
// and go-arch-lint would all treat them as part of github.com/vilaca/devpit.
// frontend/ has no Go source of its own.
module github.com/vilaca/devpit/frontend

go 1.26
