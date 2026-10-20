# Contributing

`main` holds releases, `develop` is the integration branch. Nobody pushes to either directly (branch protection applies to admins too): every change is a feature branch and a pull request.

## The cycle

1. `make dev-start NAME=<x>`: fetches `develop` and creates `feature/<x>` from it.
2. Work and commit in that branch.
3. `make dev-push [MINOR=1] [LAB=...]`: pushes the branch (it refuses on `develop` and `main` and warns about uncommitted changes), opens a PR into `develop` or updates the open one, labels it `minor` with `MINOR=1`, and turns on auto-merge: the PR merges by itself once every check is green.
4. After the merge GitHub deletes the remote branch. `make dev-done` switches to `develop`, pulls it and deletes the local branch.

Both targets use `scripts/dev.sh` and need the `gh` CLI, logged in.

## The laboratory dependency

`go.mod` is the only source of the laboratory version in every build; there is no `replace`. For local work against `../laboratory` run `make work` (it writes `../go.work`, which is gitignored; CI never uses it).

`make dev-push` handles the pin:

- without `LAB`, `go.mod` is left as it is;
- `LAB=` (empty) pins the commit `../laboratory` is on;
- `LAB=<commit|branch|vX.Y.Z>` pins that.

Before pinning it checks that `../laboratory` has no uncommitted changes and that the commit is already in laboratory `develop` on GitHub; otherwise it stops and suggests `make dev-push` in laboratory first. It warns when the pinned commit differs from the local `../laboratory`. A PR into `main` and a release are refused while `go.mod` pins a commit or a branch instead of a laboratory release; pick one up with `make dev-push LAB=vX.Y.Z`.

## Checks and images

- A PR into `develop` or `main` runs `check.yml`: build, vet and tests, and the Docker build, which writes its layers to the registry cache `cybericebox/daemon:buildcache-develop` and publishes nothing. A new push cancels the previous run. A PR from a fork builds without the cache. The required check is `Check passed`.
- A merge into `develop` (`develop.yml`) builds from that cache and publishes `sha-<7 hex of the merge commit>`. There is no moving `develop` tag.
- `Build image` (`build.yml`, manual) builds any commit, by default the `develop` head, and publishes its `sha-<7>`. When that image exists it is not rebuilt.

## Releases

- A merge of `develop` into `main` (`prerelease.yml`) publishes `vX.Y.Z-rc.N` and creates the GitHub pre-release. The version is a patch bump of the last release; it is a minor bump when any PR merged since then has the `minor` label; a major bump only through a manual run with `major`. Nothing is built when the tree is unchanged since the last rc. The rc is a retag of the develop image (no build) when the tree of `main` equals the tree of the develop head (the second parent of the merge) and that head has its `sha-<7>` image; otherwise it is built, reading the `buildcache-develop` cache.
- `Promote` (`promote.yml`, manual, input: the rc tag) checks the rc image and its checks, adds `vX.Y.Z` and `latest` to the same image without a rebuild, creates the GitHub release, deletes the `vX.Y.Z-rc.*` tags and pre-releases, and deletes the dev images: only tags that match `^sha-[0-9a-f]{7}$` whose commit is an ancestor of the release commit, plus the `buildcache-develop` and old `buildcache` cache tags (the next PR starts with a cold cache). The list is printed first; the `dry_run` input changes nothing.
- `Cleanup` (`cleanup.yml`, manual, inputs: the release `vX.Y.Z` and `dry_run`) runs the same dev-image cleanup against an existing release commit, for example when it was skipped.
- Clusters and the chart use exact tags only (`sha-*` or `vX.Y.Z`); `latest` is for outside users.

### Retention

The same rule applies to GitHub release pages and published images, in every repo.

- Git tags of final releases are kept forever, so any version can be rebuilt from source.
- Release candidates (`-rc.N`): tags, release pages and images are deleted right after the final release is promoted.
- The newest minor keeps all its patch releases and images.
- Older minors keep only their latest patch. The other patches of a minor are deleted when the next minor is released.
- Besides the newest minor, the latest patches of the 10 previous minors are kept (release page and images). An older minor drops out when it is no longer among the last 10: its release page and images are deleted, the tag stays. There are no exceptions: production always runs a current version.
- Pruning runs automatically in the release workflow. Manual cleanup is done only once, when the policy is introduced.

## Catalog metadata migration (0194)

Task metadata is versioned with the content. Existing historical versions receive the currently known name, description and tags as a baseline; past values that were never stored cannot be reconstructed.

Both production repository pools automatically declare `cybericebox.catalog_metadata_writer=1` as a PostgreSQL startup parameter. This is a binary/schema compatibility marker, not an authorization boundary. After migration 0194, connections from older binaries receive SQLSTATE `40001` (`catalog_metadata_writer capability required`) when mutating authored catalog families, attachment claims/references or catalog access. During an ordinary rolling update, catalog saves routed to an old pod may temporarily fail and can be retried after the rollout. New pods write normally; unrelated event runtime writes remain available. No deployment flag or manual activation is required.

The migration locks the protected tables through backfill and guard installation. Even capable version INSERTs must explicitly supply a nonempty Name and nonnull Description/Tags; empty Description/Tags are valid. Deliberate administrator repairs must use a transaction with `SET LOCAL cybericebox.catalog_metadata_writer = '1'` and preserve the same snapshot/family invariants. A binary-only rollback leaves old catalog writers fenced. Removing the schema guard requires an explicit schema rollback, which cannot losslessly retain metadata introduced per version; do not describe that downgrade as lossless.

An old image-import worker already running during rollout may finish uploading unreferenced immutable bytes before its first protected database mutation fails. Existing authored state and file claims/references remain protected; existing media/registry garbage collection handles unused uploads. The fence does not claim to undo external bytes.
