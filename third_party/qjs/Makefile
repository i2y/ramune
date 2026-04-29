# WASI_SDK_PATH defaults to /opt/wasi-sdk (upstream fastschema/qjs convention)
# but can be overridden via env, e.g. WASI_SDK_PATH=$HOME/.local/wasi-sdk-25
# for an Apple-Silicon-native install without touching /opt.
WASI_SDK_PATH ?= /opt/wasi-sdk

.PHONY: build build-debug clean

build:
		@echo "Configuring and building qjs (WASI_SDK_PATH=$(WASI_SDK_PATH))..."
		cd qjswasm/quickjs && \
		rm -rf build && \
		cmake -B build \
				-DQJS_BUILD_LIBC=ON \
				-DQJS_BUILD_CLI_WITH_MIMALLOC=OFF \
				-DCMAKE_TOOLCHAIN_FILE=$(WASI_SDK_PATH)/share/cmake/wasi-sdk.cmake \
				-DCMAKE_PROJECT_INCLUDE=../qjswasm.cmake
		@echo "Building qjs target..."
		make -C qjswasm/quickjs/build qjswasm -j$(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)
		@echo "Copying build/qjswasm to top-level as qjs.wasm..."
		cp qjswasm/quickjs/build/qjswasm qjs.wasm

		wasm-opt -O3 qjs.wasm -o qjs.wasm

build-debug:
		@echo "Configuring and building qjs with runtime address debug (WASI_SDK_PATH=$(WASI_SDK_PATH))..."
		cd qjswasm/quickjs && \
		rm -rf build && \
		cmake -B build \
				-DQJS_BUILD_LIBC=ON \
				-DQJS_BUILD_CLI_WITH_MIMALLOC=OFF \
				-DQJS_DEBUG_RUNTIME_ADDRESS=ON \
				-DCMAKE_TOOLCHAIN_FILE=$(WASI_SDK_PATH)/share/cmake/wasi-sdk.cmake \
				-DCMAKE_PROJECT_INCLUDE=../qjswasm.cmake
		@echo "Building qjs target..."
		make -C qjswasm/quickjs/build qjswasm -j$(shell nproc 2>/dev/null || sysctl -n hw.ncpu 2>/dev/null || echo 4)
		@echo "Copying build/qjswasm to top-level as qjs.wasm..."
		cp qjswasm/quickjs/build/qjswasm qjs.wasm

		wasm-opt -O3 qjs.wasm -o qjs.wasm

clean:
	@echo "Cleaning build directory..."
	cd quickjs && rm -rf build

test:
	./test.sh

lint:
	golangci-lint run
