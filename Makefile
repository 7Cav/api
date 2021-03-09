generate:
	buf generate
	# Generate static assets for OpenAPI UI
	statik -m -f -src third_party/OpenAPI/
#	go run proto/scripts/includetxt.go

lint:
	buf lint
	buf breaking --against 'https://github.com/7cav/api.git#branch=develop'

cert:
	cd certs; ./gen.sh; cd ..

install:
	go install \
		google.golang.org/protobuf/cmd/protoc-gen-go \
		google.golang.org/grpc/cmd/protoc-gen-go-grpc \
		github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-grpc-gateway \
		github.com/grpc-ecosystem/grpc-gateway/v2/protoc-gen-openapiv2 \
		github.com/rakyll/statik \
		github.com/bufbuild/buf/cmd/buf

evans:
	evans \
	--tls -cert certs/server-cert.pem --certkey certs/server-key.pem --cacert certs/ca-cert.pem --host localhost \
	--path /home/jarvis/.cache/buf/mod/grpc-ecosystem/grpc-gateway/240eb01580e34380ae1d138426e0174f/ \
	--path /home/jarvis/.cache/buf/mod/beta/googleapis/1dc4674e3cb949b388204fa2dc321be7 \
	--path . proto/milpacs.proto \
	-p 10000
