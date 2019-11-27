all: build install

build:
	mkdir -p ./bin
	GO111MODULE=on go build -o ./bin/kubectl-operator_dev ./cmd/kubectl-operator_dev

build-cross:
	GOOS=linux GOARCH=amd64 go build -o ./bin/kubectl-operator_dev_linux ./cmd/kubectl-operator_dev
	GOOS=darwin GOARCH=amd64 go build -o ./bin/kubectl-operator_dev_darwin ./cmd/kubectl-operator_dev

vendor:
	GO111MODULE=on go mod tidy -v
	GO111MODULE=on go mod vendor -v

install:
	cp -f ./bin/kubectl-operator_dev ${HOME}/bin/

