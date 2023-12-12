# build
build:
	go build -ldflags="-s -w" . && cp protoc-gen-swagger /Applications/www/go/bin && rm protoc-gen-swagger

