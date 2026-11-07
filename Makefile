dep:
	pip3 install flask

dummy-iface:
	# Allow UDP ping. For more information, see https://github.com/go-ping/ping#linux
	sudo sysctl -w net.ipv4.ping_group_range="0 2147483647"
	sudo ip link add dev dummy0 type dummy
	sudo ip addr add dev dummy0 192.0.2.1/24
	sudo ip addr add dev dummy0 2001:db8::1/64
	sudo ip link set dev dummy0 up

peeringdb-test-harness:
	nohup python3 tests/peeringdb/peeringdb-test-api.py &

test-setup: dummy-iface peeringdb-test-harness

test:
	export PATHVECTOR_TEST=1 && go test -v -race -coverprofile=coverage.txt -covermode=atomic ./pkg/... ./cmd/...

test-teardown:
	pkill -f tests/peeringdb/peeringdb-test-api.py
	sudo ip link del dev dummy0
	rm -f nohup.out

test-sequence: test-setup test test-teardown

# BIRD test matrix (see tests/bird-matrix/README.md)
# Builds one Docker image per BIRD version and runs the generate/validate
# tests in it. Defaults to the versions listed in tests/bird-matrix/versions.txt.
BIRD_VERSIONS ?= $(shell sed -e 's/\#.*//' tests/bird-matrix/versions.txt)
BIRD_MATRIX_DOCKERFILE ?= tests/bird-matrix/Dockerfile
BIRD_MATRIX_IMAGE ?= pathvector-bird-matrix
DOCKER_BUILD_ARGS ?=

bird-matrix:
	@# Keep going after a failing version so one run reports every failure
	@failed=""; for v in $(BIRD_VERSIONS); do \
		echo "### BIRD $$v"; \
		docker build $(DOCKER_BUILD_ARGS) -f $(BIRD_MATRIX_DOCKERFILE) \
			--build-arg BIRD_VERSION=$$v -t $(BIRD_MATRIX_IMAGE):$$v . \
		&& docker run --rm $(BIRD_MATRIX_IMAGE):$$v \
		|| failed="$$failed $$v"; \
	done; \
	if [ -n "$$failed" ]; then echo "### BIRD matrix failed for:$$failed"; exit 1; fi; \
	echo "### BIRD matrix passed for: $(strip $(BIRD_VERSIONS))"

# Same tests without Docker: builds BIRD into tests/bird-matrix/<version>/
# (needs the BIRD build dependencies, Go and python3-flask on the host)
bird-matrix-local:
	tests/bird-matrix/build-bird-versions.sh $(BIRD_VERSIONS)
	tests/bird-matrix/run-tests.sh $(addprefix tests/bird-matrix/,$(BIRD_VERSIONS))

# FRR test matrix (see tests/frr-matrix/README.md)
# Builds one Docker image per FRR version and runs the generate/validate
# tests in it. Defaults to the versions listed in tests/frr-matrix/versions.txt.
# Each line is "TAG" or "TAG LIBYANG_REF"; the optional second field builds
# that libyang from source for FRR releases that only work against an older
# libyang than the image's. Override the whole list with FRR_VERSIONS_FILE.
FRR_VERSIONS_FILE ?= tests/frr-matrix/versions.txt
FRR_MATRIX_DOCKERFILE ?= tests/frr-matrix/Dockerfile
FRR_MATRIX_IMAGE ?= pathvector-frr-matrix
# FRR_TAGS: bare version tags (install dir names). FRR_SPECS: "TAG@LIBYANGREF"
# tokens accepted by build-frr-versions.sh on the command line.
FRR_TAGS = $(shell sed -e 's/#.*//' -e '/^[[:space:]]*$$/d' $(FRR_VERSIONS_FILE) | awk '{print $$1}')
FRR_SPECS = $(shell sed -e 's/#.*//' -e '/^[[:space:]]*$$/d' $(FRR_VERSIONS_FILE) | awk '{if (NF>1) print $$1"@"$$2; else print $$1}')

frr-matrix:
	@# Keep going after a failing version so one run reports every failure.
	@# NET_ADMIN+SYS_ADMIN are required: zebra programs kernel routes over
	@# netlink and FRR's privilege separation requests cap_sys_admin.
	@failed=""; while IFS=' ' read -r v lyref rest; do \
		case "$$v" in ''|\#*) continue;; esac; \
		echo "### FRR $$v$${lyref:+ (libyang $$lyref)}"; \
		docker build $(DOCKER_BUILD_ARGS) -f $(FRR_MATRIX_DOCKERFILE) \
			--build-arg FRR_VERSION=$$v --build-arg LIBYANG_REF=$$lyref \
			-t $(FRR_MATRIX_IMAGE):$$v . \
		&& docker run --rm --cap-add NET_ADMIN --cap-add SYS_ADMIN $(FRR_MATRIX_IMAGE):$$v \
		|| failed="$$failed $$v"; \
	done < $(FRR_VERSIONS_FILE); \
	if [ -n "$$failed" ]; then echo "### FRR matrix failed for:$$failed"; exit 1; fi; \
	echo "### FRR matrix passed"

# Same tests without Docker: builds FRR into tests/frr-matrix/<version>/root/
# (needs the FRR build dependencies, Go and python3-flask on the host)
frr-matrix-local:
	tests/frr-matrix/build-frr-versions.sh $(FRR_SPECS)
	tests/frr-matrix/run-tests.sh $(addsuffix /root,$(addprefix tests/frr-matrix/,$(FRR_TAGS)))

.PHONY: bird-matrix bird-matrix-local frr-matrix frr-matrix-local

e2e:
	bash tests/e2e/run-tests.sh
