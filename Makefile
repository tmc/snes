.PHONY: test qualify

test:
	go test -mod=readonly ./... -count=1 -timeout=120s
	go vet -mod=readonly ./...

qualify:
	@test -n "$(MANIFEST)" -a -n "$(OUT)" || { echo 'usage: make qualify MANIFEST=manifest.json OUT=receipt-directory'; exit 2; }
	go run -mod=readonly ./cmd/snesqualify -manifest "$(MANIFEST)" -out "$(OUT)"
