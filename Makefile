.PHONY: all clean

BINARY_NAME := tcp-nvmf-io
BUILD_DIR := build
SOURCE := examples/tcp-nvmf-io/main.go
TARGET := $(BUILD_DIR)/$(BINARY_NAME)

all: $(TARGET)

$(TARGET): $(SOURCE)
	@mkdir -p $(BUILD_DIR)
	go build -o $@ ./examples/tcp-nvmf-io

clean:
	rm -rf $(BUILD_DIR)
