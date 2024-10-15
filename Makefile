sqlcGenerate:
	docker run --rm -v ./internal/delivery/repository/postgres:/src -w /src sqlc/sqlc generate

addMigration:
	migrate create -ext sql -dir internal/delivery/repository/postgres/migrations -seq $(name)

buildAndPush:
	docker build -f deploy/Dockerfile . -t cybericebox/daemon:$(tag) && docker push cybericebox/daemon:$(tag)

swagger:
	swag init -g handler.go -o ./internal/delivery/controller/http/handler/docs -d ./internal/delivery/controller/http/handler

updatePackages:
	go get -u ./...