load("@io_bazel_rules_go//go:def.bzl", "go_library")
load("@io_bazel_rules_go//proto:def.bzl", "go_proto_library")

proto_library(
    name = "any_proto",
    srcs = ["any.proto"],
    visibility = ["//visibility:public"],
)

go_proto_library(
    name = "any_go_proto",
    importpath = "github.com/golang/protobuf/ptypes/any",
    proto = ":any_proto",
    visibility = ["//visibility:public"],
)

go_library(
    name = "go_default_library",
    embed = [":any_go_proto"],
    importpath = "github.com/golang/protobuf/ptypes/any",
    visibility = ["//visibility:public"],
)

filegroup(
    name = "package-srcs",
    srcs = glob(["**"]),
    tags = ["automanaged"],
    visibility = ["//visibility:private"],
)

filegroup(
    name = "all-srcs",
    srcs = [":package-srcs"],
    tags = ["automanaged"],
    visibility = ["//visibility:public"],
)
