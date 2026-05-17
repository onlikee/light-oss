import fs from "node:fs";
import os from "node:os";
import path from "node:path";
import { afterEach, describe, expect, it } from "vitest";
import { applyRootDefaultEnv, loadRootDefaultEnv } from "./root-env";

describe("loadRootDefaultEnv", () => {
  const tempDirs: string[] = [];

  afterEach(() => {
    while (tempDirs.length > 0) {
      const dir = tempDirs.pop();
      if (dir) {
        fs.rmSync(dir, { recursive: true, force: true });
      }
    }
  });

  it("uses .env.personal when present", () => {
    const rootDir = createTempRootDir(tempDirs);
    writeEnvFile(
      rootDir,
      ".env",
      [
        "VITE_DEFAULT_API_BASE_URL=http://from-root",
        "VITE_DEFAULT_BEARER_TOKEN=root-token",
      ].join("\n"),
    );
    writeEnvFile(
      rootDir,
      ".env.personal",
      [
        "VITE_DEFAULT_API_BASE_URL=http://from-personal",
        "VITE_DEFAULT_BEARER_TOKEN=personal-token",
      ].join("\n"),
    );

    expect(loadRootDefaultEnv(rootDir)).toEqual({
      VITE_DEFAULT_API_BASE_URL: "http://from-personal",
      VITE_DEFAULT_BEARER_TOKEN: "personal-token",
    });
  });

  it("does not fall back to .env when .env.personal omits a key", () => {
    const rootDir = createTempRootDir(tempDirs);
    writeEnvFile(
      rootDir,
      ".env",
      [
        "VITE_DEFAULT_API_BASE_URL=http://from-root",
        "VITE_DEFAULT_BEARER_TOKEN=root-token",
      ].join("\n"),
    );
    writeEnvFile(
      rootDir,
      ".env.personal",
      "VITE_DEFAULT_API_BASE_URL=http://from-personal",
    );

    expect(loadRootDefaultEnv(rootDir)).toEqual({
      VITE_DEFAULT_API_BASE_URL: "http://from-personal",
    });
  });

  it("falls back to .env when .env.personal is missing", () => {
    const rootDir = createTempRootDir(tempDirs);
    writeEnvFile(
      rootDir,
      ".env",
      [
        "VITE_DEFAULT_API_BASE_URL=http://from-root",
        "VITE_DEFAULT_BEARER_TOKEN=root-token",
      ].join("\n"),
    );

    expect(loadRootDefaultEnv(rootDir)).toEqual({
      VITE_DEFAULT_API_BASE_URL: "http://from-root",
      VITE_DEFAULT_BEARER_TOKEN: "root-token",
    });
  });

  it("strips inline comments from unquoted values", () => {
    const rootDir = createTempRootDir(tempDirs);
    writeEnvFile(
      rootDir,
      ".env.personal",
      [
        "VITE_DEFAULT_API_BASE_URL=http://from-personal # local api",
        "VITE_DEFAULT_BEARER_TOKEN=personal-token # local token",
      ].join("\n"),
    );

    expect(loadRootDefaultEnv(rootDir)).toEqual({
      VITE_DEFAULT_API_BASE_URL: "http://from-personal",
      VITE_DEFAULT_BEARER_TOKEN: "personal-token",
    });
  });

  it("keeps hash characters inside quoted values", () => {
    const rootDir = createTempRootDir(tempDirs);
    writeEnvFile(
      rootDir,
      ".env.personal",
      [
        'VITE_DEFAULT_API_BASE_URL="http://from-personal/#fragment"',
        "VITE_DEFAULT_BEARER_TOKEN='token-with-#-suffix'",
      ].join("\n"),
    );

    expect(loadRootDefaultEnv(rootDir)).toEqual({
      VITE_DEFAULT_API_BASE_URL: "http://from-personal/#fragment",
      VITE_DEFAULT_BEARER_TOKEN: "token-with-#-suffix",
    });
  });

  it("returns an empty object when neither root env file exists", () => {
    const rootDir = createTempRootDir(tempDirs);

    expect(loadRootDefaultEnv(rootDir)).toEqual({});
  });
});

describe("applyRootDefaultEnv", () => {
  const tempDirs: string[] = [];

  afterEach(() => {
    while (tempDirs.length > 0) {
      const dir = tempDirs.pop();
      if (dir) {
        fs.rmSync(dir, { recursive: true, force: true });
      }
    }
  });

  it("keeps explicit process env values and only fills missing keys", () => {
    const rootDir = createTempRootDir(tempDirs);
    writeEnvFile(
      rootDir,
      ".env.personal",
      [
        "VITE_DEFAULT_API_BASE_URL=http://from-personal",
        "VITE_DEFAULT_BEARER_TOKEN=personal-token",
      ].join("\n"),
    );

    const env: NodeJS.ProcessEnv = {
      VITE_DEFAULT_API_BASE_URL: "http://from-shell",
    };

    applyRootDefaultEnv(rootDir, env);

    expect(env).toEqual({
      VITE_DEFAULT_API_BASE_URL: "http://from-shell",
      VITE_DEFAULT_BEARER_TOKEN: "personal-token",
    });
  });
});

function createTempRootDir(tempDirs: string[]): string {
  const rootDir = fs.mkdtempSync(path.join(os.tmpdir(), "light-oss-root-env-"));
  tempDirs.push(rootDir);
  return rootDir;
}

function writeEnvFile(rootDir: string, filename: string, contents: string) {
  fs.writeFileSync(path.join(rootDir, filename), contents, "utf8");
}
