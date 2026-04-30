import { describe, expect, it } from "vitest";
import { storagePayload, targetPayload, type StorageForm, type TargetForm } from "./App";

const baseTarget: TargetForm = {
  id: "target-1",
  name: "Primary target",
  driver: "redis",
  endpoint: "127.0.0.1:6379",
  database: "",
  username: "",
  password: "",
  tls: "",
  agent: "",
  tier: "",
};

const baseStorage: StorageForm = {
  id: "storage-1",
  name: "Primary storage",
  kind: "local",
  uri: "file:///tmp/kronos-repo",
  region: "",
  endpoint: "",
  credentials: "",
  accessKey: "",
  secretKey: "",
  sessionToken: "",
  forcePathStyle: false,
};

describe("targetPayload", () => {
  it("does not send a database for redis targets", () => {
    const payload = targetPayload(
      {
        ...baseTarget,
        database: "should-not-leak",
        username: "default",
        password: "secret",
      },
      null,
    );

    expect(payload).toMatchObject({
      id: "target-1",
      name: "Primary target",
      driver: "redis",
      endpoint: "127.0.0.1:6379",
      options: {
        username: "default",
        password: "secret",
      },
    });
    expect(payload).not.toHaveProperty("database");
  });

  it("sends database and labels for SQL-style targets", () => {
    const payload = targetPayload(
      {
        ...baseTarget,
        driver: "postgres",
        endpoint: "db.example.com:5432",
        database: "app",
        tls: "require",
        agent: "worker-a",
        tier: "gold",
      },
      "target-existing",
    );

    expect(payload).toMatchObject({
      id: "target-existing",
      name: "Primary target",
      driver: "postgres",
      endpoint: "db.example.com:5432",
      database: "app",
      options: {
        tls: "require",
      },
      labels: {
        agent: "worker-a",
        tier: "gold",
      },
    });
  });
});

describe("storagePayload", () => {
  it("does not send S3 options for local storage", () => {
    const payload = storagePayload(
      {
        ...baseStorage,
        region: "us-east-1",
        endpoint: "https://s3.example.com",
        credentials: "env",
        accessKey: "access",
        secretKey: "secret",
        sessionToken: "token",
        forcePathStyle: true,
      },
      null,
    );

    expect(payload).toEqual({
      id: "storage-1",
      name: "Primary storage",
      kind: "local",
      uri: "file:///tmp/kronos-repo",
    });
  });

  it("sends S3 options only for S3 storage", () => {
    const payload = storagePayload(
      {
        ...baseStorage,
        kind: "s3",
        uri: "s3://kronos/backups",
        region: "us-east-1",
        endpoint: "https://s3.example.com",
        credentials: "env",
        accessKey: "access",
        secretKey: "secret",
        sessionToken: "token",
        forcePathStyle: true,
      },
      "storage-existing",
    );

    expect(payload).toEqual({
      id: "storage-existing",
      name: "Primary storage",
      kind: "s3",
      uri: "s3://kronos/backups",
      options: {
        region: "us-east-1",
        endpoint: "https://s3.example.com",
        credentials: "env",
        access_key: "access",
        secret_key: "secret",
        session_token: "token",
        force_path_style: true,
      },
    });
  });
});
