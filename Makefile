# Pinned to the swaggo/swag version in go.mod. Run via go run <pkg>@<version> so
# the tool resolves its own CLI dependencies without polluting the app's go.mod.
SWAG_VERSION    := v1.16.6
SWAG_ENTRYPOINT := doc.go
HANDLER_DIR     := ./internal/delivery/controller/http/handler
RESPONSE_DIR    := ./internal/delivery/controller/http/response
APIDOCS_DIR     := ./internal/delivery/controller/http/handler/apidocs

.PHONY: swagger build vet test tidy run sqlcGenerate error-catalog error-catalog-check seed

sqlcGenerate:
	@docker run --rm -v ./internal/delivery/repository/postgres:/src -w /src sqlc/sqlc:1.31.1@sha256:70f53171d27b2424e9358869975455a6e955a5aa8e58a998a270a6e34e525537 generate

## error-catalog: regenerate error-catalog/errors.en.json (code → English message,
## the single source of truth frontends localize against). Run after adding or
## renaming a domain error, then add the Ukrainian text to errors.uk.json.
## GOWORK=off: the parent go.work does not list .worktrees/* checkouts, so without
## it `go run` fails there.
error-catalog:
	@GOWORK=off go run ./tools/errorcatalog > error-catalog/errors.en.json.tmp && mv error-catalog/errors.en.json.tmp error-catalog/errors.en.json
	@echo "error-catalog/errors.en.json regenerated"

## error-catalog-check: fail when the committed catalog drifts from the code
## (en stale, uk and en codes differ, or an ASCII apostrophe in uk).
error-catalog-check:
	@GOWORK=off go test ./tools/errorcatalog

## swagger: regenerate the OpenAPI/Swagger spec from handler godoc annotations.
## RESPONSE_DIR is included so swag can resolve the shared response.Response type;
## --parseDependencyLevel 1 resolves the model/useCase types DTOs embed.
swagger:
	go run github.com/swaggo/swag/cmd/swag@$(SWAG_VERSION) init \
		-g $(SWAG_ENTRYPOINT) \
		-d $(HANDLER_DIR),$(RESPONSE_DIR),./internal/model/rbac,./internal/model/user,./internal/useCase/auth,./pkg/pagination \
		-o $(APIDOCS_DIR) \
		--parseInternal \
		--parseDependency --parseDependencyLevel 1

## build: compile all packages
build:
	@go build ./...

## vet: run go vet over all packages + error-code lint
vet:
	@go vet ./...
	@go run ./tools/checkerrorcodes internal pkg
	@go run ./tools/checklayers internal/model
	@go run ./tools/checkroutes

## test: run the full test suite
test:
	@go test ./...

## tidy: tidy go.mod/go.sum
tidy:
	@go mod tidy

## run: start the daemon
run:
	@go run ./cmd/daemon

## run-local: load ./.env into the environment, then start the daemon (host dev).
## The daemon reads config from the process env only (no godotenv), so this
## sources .env first. Pair with `make host-up` in ../../infrastructure/local.
run-local:
	@set -a; . ./.env; set +a; go run ./cmd/daemon

## lint-errors: fail on duplicate error DetailCodes within one object code
lint-errors:
	@go run ./tools/checkerrorcodes internal pkg

## lint-layers: fail when internal/model imports application/delivery packages
lint-layers:
	@go run ./tools/checklayers internal/model

## lint-routes: fail on handler routes lacking a RequirePermission gate
lint-routes:
	@go run ./tools/checkroutes

## seed: fill the development database with test data for live checks, or remove it
## (make seed ARGS="--delete"). Dev only: refuses ENV=production. Reads ./.env like run-local;
## accounts and their password go to the gitignored .seed-credentials. See the README.
seed:
	@set -a; . ./.env; set +a; go run ./cmd/seed $(ARGS)

# Branch cycle (scripts/dev.sh, the same in every repository), see CONTRIBUTING.md:
#   make dev-start NAME=<x>      feature/<x> from the fresh develop
#   make dev-push [MINOR=1] [LAB=...]   push, open or update the PR into develop, auto-merge when green
#       LAB absent: go.mod keeps its laboratory pin; LAB= (empty): pin the commit ../laboratory is on;
#       LAB=<commit|branch|vX.Y.Z>: pin that (see scripts/dev-pre-push.sh)
#   make dev-done                back to develop, pull, delete the merged local branch
#   make work                    ../go.work: build against the local ../laboratory (gitignored; CI never uses it)
.PHONY: dev-start dev-push dev-done work
dev-start:
	@NAME='$(NAME)' scripts/dev.sh start

dev-push:
	@LAB_SET='$(if $(filter command line,$(origin LAB)),1)' LAB='$(LAB)' MINOR='$(MINOR)' scripts/dev.sh push

dev-done:
	@scripts/dev.sh done

work:
	@test -d ../laboratory || { echo "../laboratory is not checked out" >&2; exit 1; }
	@test ! -e ../go.work || { echo "../go.work already exists" >&2; exit 1; }
	@v=$$(.github/scripts/lab-pin.sh version); cd .. && go work init ./daemon ./laboratory && go work edit -replace "github.com/cybericebox/laboratory@$$v=./laboratory" && echo "../go.work created: the daemon builds against ../laboratory (rerun after changing the pin: rm ../go.work && make work)"
