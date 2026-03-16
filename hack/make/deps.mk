KUBECTL1_VERSION := v1.30.8
KUBECTL1_SUM_arm64 ?= $(shell curl -L "https://dl.k8s.io/release/$(KUBECTL1_VERSION)/bin/linux/arm64/kubectl.sha256")
KUBECTL1_SUM_amd64 ?= $(shell curl -L "https://dl.k8s.io/release/$(KUBECTL1_VERSION)/bin/linux/amd64/kubectl.sha256")

KUBECTL2_VERSION := v1.31.4
KUBECTL2_SUM_arm64 ?= $(shell curl -L "https://dl.k8s.io/release/$(KUBECTL2_VERSION)/bin/linux/arm64/kubectl.sha256")
KUBECTL2_SUM_amd64 ?= $(shell curl -L "https://dl.k8s.io/release/$(KUBECTL2_VERSION)/bin/linux/amd64/kubectl.sha256")

KUBECTL3_VERSION := v1.32.0
KUBECTL3_SUM_arm64 ?= $(shell curl -L "https://dl.k8s.io/release/$(KUBECTL3_VERSION)/bin/linux/arm64/kubectl.sha256")
KUBECTL3_SUM_amd64 ?= $(shell curl -L "https://dl.k8s.io/release/$(KUBECTL3_VERSION)/bin/linux/amd64/kubectl.sha256")

KUBECTL1 := "$(KUBECTL1_VERSION):$(KUBECTL1_SUM_arm64):$(KUBECTL1_SUM_amd64)"
KUBECTL2 := "$(KUBECTL2_VERSION):$(KUBECTL2_SUM_arm64):$(KUBECTL2_SUM_amd64)"
KUBECTL3 := "$(KUBECTL3_VERSION):$(KUBECTL3_SUM_arm64):$(KUBECTL3_SUM_amd64)"

KUBECTL_VERSION_INFO := "$(KUBECTL1) $(KUBECTL2) $(KUBECTL3)"

# Reduces the code duplication on Makefile by keeping all args into a single variable.
IMAGE_ARGS := --build-arg KUBECTL_VERSION_INFO=$(KUBECTL_VERSION_INFO)