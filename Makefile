GREEN := \033[0;32m
RED   := \033[0;31m
RESET := \033[0m

.PHONY: run build clean test
.ONESHELL:

run:
	go run . || true

build:
	go build -o container .

clean:
	rm -f container

test: build
	@n=0
	run_test() {
		n=$$((n + 1))
		printf '$(GREEN)TEST[%d]$(RESET)\n' "$$n"
		eval "$$1" || true
	}
	run_test "./container"
	run_test "./container does_not_exists"
	run_test "./container run _not_existing_image_"
	run_test "./container run alpine /bin/ls"
