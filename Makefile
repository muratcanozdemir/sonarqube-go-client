.PHONY: generate modcheck test cover lint integration-up integration build

generate: ## Regenerate the client from docs/ and spec/
	cd tools/codegen && go run . -root ../..

modcheck: ## Fail if any tracked path would break the module zip
	git -c core.quotepath=off ls-files | (cd tools/modcheck && go run .)

test: ## Unit tests with the race detector
	go test -race ./...

cover: ## Library coverage (must be 100%)
	go test -coverprofile=coverage.out ./sonarqube && go tool cover -func=coverage.out | tail -1

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.15.0 run ./...

SQ_VERSION ?= 26.11.0.134175
integration-up: ## Start a Community Edition server and seed it (writes .it.env)
	docker run -d --name sqgo-it -p 9000:9000 -e SONAR_ES_BOOTSTRAP_CHECKS_DISABLE=true \
	  -e SONAR_WEB_SYSTEMPASSCODE=it-passcode sonarqube:$(SQ_VERSION)-community
	SONARQUBE_PASSCODE=it-passcode ./scripts/it-seed.sh .it.env

integration: ## Run the integration suite against the server in .it.env
	set -a; . ./.it.env; set +a; go test -count=1 -tags integration $(if $(SHORT),-short) ./integration

build:
	CGO_ENABLED=0 go build -trimpath -o dist/sonarqube-cli ./cmd/sonarqube-cli
