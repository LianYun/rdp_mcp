cmake_minimum_required(VERSION 3.13)
project(remoter-worker LANGUAGES C)

set(CMAKE_C_STANDARD 11)
set(CMAKE_C_STANDARD_REQUIRED ON)
if(NOT CMAKE_BUILD_TYPE)
  set(CMAKE_BUILD_TYPE RelWithDebInfo)
endif()

find_package(PkgConfig REQUIRED)
pkg_check_modules(FREERDP REQUIRED IMPORTED_TARGET freerdp3>=3.5 freerdp-client3>=3.5 winpr3>=3.5)

add_executable(remoter-worker
  src/main.c
  src/ipc.c
  src/rdp.c
  src/clipboard.c
  src/pointer.c
)
target_link_libraries(remoter-worker PRIVATE PkgConfig::FREERDP)
target_compile_options(remoter-worker PRIVATE -Wall -Wextra -Wno-unused-parameter)

if(APPLE)
  set_target_properties(remoter-worker PROPERTIES INSTALL_RPATH "@executable_path/../lib")
else()
  set_target_properties(remoter-worker PROPERTIES INSTALL_RPATH "$ORIGIN/../lib")
endif()
install(TARGETS remoter-worker RUNTIME DESTINATION bin)
