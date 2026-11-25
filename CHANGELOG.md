# Changelog

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project aims to adhere to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Changed

- **Breaking: `CreateVM` refuses any VM spec carrying a secret** (inline
  or reference) with the constant error `ErrSecretsUnsupported`. The
  driver has no vsock channel to deliver secrets safely and never falls
  back to inlining them. `drivers.VMSpec` has no dedicated secret field,
  so `RejectSecrets` inspects `Cmdline` and `Name` for secret-named keys
  (secret, private_key, access_key, wireguard) and raw WireGuard keys.
