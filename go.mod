module github.com/v0x3ll3m0n/ha-power

go 1.26.11

require github.com/prusa3d/prusa-connect/sdk v0.0.0

require gopkg.in/yaml.v3 v3.0.1 // indirect

// Until the SDK is published as a Go module, it is built from the pinned
// Prusa Connect checkout that `make core` puts in .core/ (see CORE_REF in
// the Makefile). Delete these lines once `go get` can fetch it.
replace github.com/prusa3d/prusa-connect/sdk => ./.core/sdk
