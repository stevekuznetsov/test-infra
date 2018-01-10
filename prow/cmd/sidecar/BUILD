load("@io_bazel_rules_go//go:def.bzl", "go_binary", "go_library")

go_library(
    name = "go_default_library",
    srcs = ["main.go"],
    importpath = "k8s.io/test-infra/prow/cmd/sidecar",
    visibility = ["//visibility:private"],
    deps = [
        "//prow/pod-utils/gcs:go_default_library",
        "//prow/pod-utils/refs:go_default_library",
        "//vendor/cloud.google.com/go/storage:go_default_library",
        "//vendor/github.com/fsnotify/fsnotify:go_default_library",
        "//vendor/github.com/sirupsen/logrus:go_default_library",
        "//vendor/google.golang.org/api/option:go_default_library",
    ],
)

go_binary(
    name = "sidecar",
    embed = [":go_default_library"],
    importpath = "k8s.io/test-infra/prow/cmd/sidecar",
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
