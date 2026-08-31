import { describe, expect, it } from "vitest";
import {
  ENDPOINT_PRESETS,
  keyShapeError,
  presetById,
  validateBaseUrl,
} from "./endpoints";

describe("endpoint presets", () => {
  it("offers the five hosted presets plus another-service and on-this-computer", () => {
    expect(ENDPOINT_PRESETS.map((p) => p.id)).toEqual([
      "anthropic",
      "openai",
      "openrouter",
      "github",
      "azure",
      "custom",
      "local",
    ]);
  });

  it("classifies only the on-this-computer preset as local", () => {
    expect(ENDPOINT_PRESETS.filter((p) => p.local).map((p) => p.id)).toEqual(["local"]);
    expect(presetById("local").requireLoopback).toBe(true);
    expect(presetById("local").needsKey).toBe(false);
  });

  it("pins fixed base URLs for the hosted presets and collects one for azure", () => {
    expect(presetById("github").baseUrl).toBe("https://models.github.ai/inference");
    expect(presetById("azure").baseUrl).toBeUndefined();
    expect(presetById("azure").needsBaseUrl).toBe(true);
  });
});

describe("validateBaseUrl", () => {
  it("accepts https and normalizes a trailing slash", () => {
    expect(validateBaseUrl("https://api.example.com/v1/")).toEqual({
      ok: true,
      url: "https://api.example.com/v1",
    });
  });

  it("rejects plain http except on loopback", () => {
    expect(validateBaseUrl("http://api.example.com/v1").ok).toBe(false);
    expect(validateBaseUrl("http://localhost:11434/v1").ok).toBe(true);
    expect(validateBaseUrl("http://127.0.0.1:8080/v1").ok).toBe(true);
  });

  it("rejects userinfo and non-URLs", () => {
    expect(validateBaseUrl("https://user:pw@example.com").ok).toBe(false);
    expect(validateBaseUrl("not a url").ok).toBe(false);
    expect(validateBaseUrl("").ok).toBe(false);
  });

  it("holds on-this-computer to loopback — a remote URL is refused, not reclassified", () => {
    expect(validateBaseUrl("https://api.example.com", { requireLoopback: true }).ok).toBe(
      false,
    );
    expect(
      validateBaseUrl("http://localhost:11434/v1", { requireLoopback: true }).ok,
    ).toBe(true);
  });
});

describe("keyShapeError", () => {
  it("catches a wrong-string paste instantly", () => {
    expect(keyShapeError(presetById("anthropic"), "xoxb-123")).toMatch(/sk-ant-/);
    expect(keyShapeError(presetById("anthropic"), "sk-ant-abc")).toBeNull();
  });

  it("accepts either GitHub token shape", () => {
    expect(keyShapeError(presetById("github"), "github_pat_x")).toBeNull();
    expect(keyShapeError(presetById("github"), "ghp_x")).toBeNull();
    expect(keyShapeError(presetById("github"), "sk-x")).toMatch(/github_pat_/);
  });

  it("stays quiet where no shape is known (azure, custom) and on empty input", () => {
    expect(keyShapeError(presetById("azure"), "anything")).toBeNull();
    expect(keyShapeError(presetById("custom"), "anything")).toBeNull();
    expect(keyShapeError(presetById("anthropic"), "")).toBeNull();
  });
});
