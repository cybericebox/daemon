sqlcGenerate:
	docker run --rm -v ./internal/delivery/repository/postgres:/src -w /src sqlc/sqlc generate

addMigration:
	migrate create -ext sql -dir internal/delivery/repository/postgres/migrations -seq $(name)

buildAndPush:
	docker build -f deploy/Dockerfile . -t cybericebox/daemon:$(tag) && docker push cybericebox/daemon:$(tag)
