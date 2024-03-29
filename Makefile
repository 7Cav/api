generate:
	buf generate
	# Generate static assets for OpenAPI UI
	statik -m -f -src third_party/OpenAPI/

lint:
	buf lint
	buf breaking --against 'https://github.com/7cav/api.git#branch=develop'

certs:
	rm -rf out/
	certstrap init --common-name "ExampleCA" --passphrase ""
	certstrap request-cert --common-name localhost --ip 0.0.0.0,127.0.0.1 --passphrase ""
	certstrap sign localhost --CA "ExampleCA"

install:
	go install \
		google.golang.org/protobuf/cmd/protoc-gen-go \
		google.golang.org/grpc/cmd/protoc-gen-go-grpc \
		github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway \
		github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2 \
		github.com/rakyll/statik
	go get -u \
		github.com/bufbuild/buf/cmd/buf \
		github.com/square/certstrap \
		github.com/spf13/cobra

evans:
	evans \
	--tls -cert out/localhost.crt --certkey out/localhost.key --cacert out/ExampleCA.crt \
	--path /home/jarvis/.cache/buf/mod/grpc-ecosystem/grpc-gateway/240eb01580e34380ae1d138426e0174f/ \
	--path /home/jarvis/.cache/buf/mod/beta/googleapis/1dc4674e3cb949b388204fa2dc321be7 \
	--path . proto/milpacs.proto \
	-p 10000
