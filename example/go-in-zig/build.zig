const std = @import("std");

pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});

    // ── Step 1: Build Zig shared library → zig-out/lib/go_in_zig.dll ──
    const lib = b.addLibrary(.{
        .name = "go_in_zig",
        .linkage = .dynamic,
        .root_module = b.createModule(.{
            .root_source_file = b.path("src/root.zig"),
            .target = target,
            .optimize = optimize,
            .link_libc = true,
        }),
    });
    const install_lib = b.addInstallArtifact(lib, .{});

    // ── Step 2: Build Go binary that links to the Zig library ──
    const go_build = b.addSystemCommand(&[_][]const u8{
        "go", "build",
        "-o", "zig-out/bin/go_in_zig_project.exe",
        ".",
    });
    go_build.setEnvironmentVariable("CGO_ENABLED", "1");
    go_build.setEnvironmentVariable("CGO_CFLAGS", "-Izig-out/include");
    go_build.setEnvironmentVariable("CGO_LDFLAGS", "-Lzig-out/lib -lgo_in_zig");

    // Go build must run after Zig library is installed
    go_build.step.dependOn(&install_lib.step);

    // ── Step 3: Custom "run" step: zig build run ──
    const run_cmd = b.addSystemCommand(&[_][]const u8{
        "zig-out/bin/go_in_zig_project.exe",
    });
    run_cmd.step.dependOn(&go_build.step);

    const run_step = b.step("run", "zig build -> go build -> run demo");
    run_step.dependOn(&run_cmd.step);

    // ── Step 4: Top-level install = Zig lib + Go exe ──
    b.getInstallStep().dependOn(&go_build.step);
}
