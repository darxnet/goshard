//go:build !(386 || amd64 || loong64 || riscv64 || wasm || arm || mips || mipsle || mips64 || mips64le || s390x)

package goshard

// 128 B covers arm64 (Apple silicon uses 128 B lines) and ppc64, and is a
// safe upper bound for architectures not listed in the other cacheline files.
const cacheLineSize = 128
