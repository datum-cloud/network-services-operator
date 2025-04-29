### Changed

- Added `transportservers` resource to ClusterRole when specifying `f5-transportserver` or `f5-virtualserver` as a source. ([#5066](https://github.com/kubernetes-sigs/external-dns/pull/5066)) _@visokoo_

### Fixed

- Fixed handling of non-string types in `serviceAccount.metadata.annotations` field. ([#5067](https://github.com/kubernetes-sigs/external-dns/pull/5067)) _@hjoshi123_
- Fixed regression where `affinity.nodeAffinity` was being ignored. ([#5046](https://github.com/kubernetes-sigs/external-dns/pull/5046)) _@mkhpalm_
