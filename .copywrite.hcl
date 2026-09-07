schema_version = 1

project {
  license          = "MPL-2.0"
  copyright_holder = "Savely Krasovsky"
  copyright_year   = 2025
  ignore_year1     = true

  header_ignore = [
    # Preserve original HashiCorp notices alongside local attribution.
    # copywrite would replace the upstream holder with the configured one.
    "LICENSE",
    "main.go",
    "tools/tools.go",
    "internal/provider/provider.go",
    "internal/provider/provider_test.go",
    "internal/provider/dirhash_function.go",
    "internal/provider/dirhash_function_test.go",
    "internal/provider/dirset_function.go",
    "internal/provider/dirset_function_test.go",

    # local IDE state and generated build output
    ".idea/**",
    "bin/**",
    "dist/**",

    # internal catalog metadata (prose)
    "META.d/**/*.yaml",

    # examples used within documentation (prose)
    "examples/**",

    # GitHub issue template configuration
    ".github/ISSUE_TEMPLATE/*.yml",

    # golangci-lint tooling configuration
    ".golangci.yml",

    # GoReleaser tooling configuration
    ".goreleaser.yml",
  ]
}
