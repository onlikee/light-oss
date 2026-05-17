import { beforeEach, describe, expect, it, vi } from "vitest";
import type { AppSettings } from "../lib/settings";
import {
  buildPublicObjectURL,
  checkObjectExists,
  deleteRecycleBinObjects,
  deleteExplorerEntriesBatch,
  deleteFolder,
  downloadFolderZip,
  listRecycleBinObjects,
  restoreRecycleBinObjects,
  updateObjectVisibility,
  uploadFolder,
  uploadObject,
} from "./objects";

const request = vi.fn();
const apiRequestMock = vi.fn();

vi.mock("./client", () => ({
  ApiError: class ApiError extends Error {
    status: number;
    code?: string;

    constructor(message: string, status: number, code?: string) {
      super(message);
      this.name = "ApiError";
      this.status = status;
      this.code = code;
    }
  },
  apiRequest: (...args: unknown[]) => apiRequestMock(...args),
  createApiClient: vi.fn(() => ({
    request,
  })),
}));

const settings: AppSettings = {
  apiBaseUrl: "https://api.localhost",
  bearerToken: "dev-token",
};

describe("objects api helpers", () => {
  beforeEach(() => {
    request.mockReset();
    apiRequestMock.mockReset();
    request.mockResolvedValue({
      data: {
        data: {
          id: 1,
        },
      },
    });
    apiRequestMock.mockResolvedValue({
      id: 1,
    });
    vi.stubGlobal("URL", {
      createObjectURL: vi.fn(() => "blob:folder-zip"),
      revokeObjectURL: vi.fn(),
    });
    HTMLAnchorElement.prototype.click = vi.fn();
  });

  it("encodes dots in object keys for upload requests", async () => {
    const file = new File(["sql"], "sample.postgresql.sql", {
      type: "application/sql",
    });

    await uploadObject(settings, {
      bucket: "demo",
      objectKey: "docs/sample.postgresql.sql",
      file,
      visibility: "private",
    });

    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        timeout: 0,
        url: "/api/v1/buckets/demo/objects/docs/sample%2Epostgresql%2Esql",
        headers: expect.objectContaining({
          "X-Allow-Overwrite": "false",
        }),
      }),
    );
  });

  it("checks object existence with HEAD requests", async () => {
    await expect(
      checkObjectExists(settings, "demo", "docs/sample.postgresql.sql"),
    ).resolves.toBe(true);

    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        method: "HEAD",
        url: "/api/v1/buckets/demo/objects/docs/sample%2Epostgresql%2Esql",
      }),
    );
  });

  it("returns false for missing objects in HEAD checks", async () => {
    request.mockRejectedValueOnce({
      isAxiosError: true,
      message: "Request failed with status code 404",
      response: {
        status: 404,
        data: {
          error: {
            code: "object_not_found",
            message: "object not found",
          },
        },
      },
    });

    await expect(
      checkObjectExists(settings, "demo", "docs/missing.txt"),
    ).resolves.toBe(false);
  });

  it("sets overwrite header when explicit overwrite is requested", async () => {
    const file = new File(["sql"], "sample.postgresql.sql", {
      type: "application/sql",
    });

    await uploadObject(settings, {
      bucket: "demo",
      objectKey: "docs/sample.postgresql.sql",
      file,
      visibility: "private",
      allowOverwrite: true,
    });

    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        headers: expect.objectContaining({
          "X-Allow-Overwrite": "true",
        }),
      }),
    );
  });

  it("normalizes upload conflict responses into ApiError with code", async () => {
    request.mockRejectedValueOnce({
      isAxiosError: true,
      message: "Request failed with status code 409",
      response: {
        status: 409,
        data: {
          error: {
            code: "object_exists",
            message: "object already exists",
          },
        },
      },
    });

    const file = new File(["sql"], "sample.postgresql.sql", {
      type: "application/sql",
    });

    await expect(
      uploadObject(settings, {
        bucket: "demo",
        objectKey: "docs/sample.postgresql.sql",
        file,
        visibility: "private",
      }),
    ).rejects.toMatchObject({
      status: 409,
      code: "object_exists",
      message: "object already exists",
    });
  });

  it("falls back to text/markdown for markdown files without a browser mime type", async () => {
    const file = new File(["# hello"], "test.md");

    await uploadObject(settings, {
      bucket: "demo",
      objectKey: "docs/test.md",
      file,
      visibility: "private",
    });

    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        headers: expect.objectContaining({
          "Content-Type": "text/markdown",
        }),
      }),
    );
  });

  it("encodes dots when building a public object URL", () => {
    expect(
      buildPublicObjectURL(
        "https://api.localhost/",
        "demo",
        "docs/sample.postgresql.sql",
      ),
    ).toBe(
      "https://api.localhost/api/v1/buckets/demo/objects/docs/sample%2Epostgresql%2Esql",
    );
  });

  it("adds the download query when building a public object download URL", () => {
    expect(
      buildPublicObjectURL(
        "https://api.localhost/",
        "demo",
        "docs/sample.postgresql.sql",
        { download: true },
      ),
    ).toBe(
      "https://api.localhost/api/v1/buckets/demo/objects/docs/sample%2Epostgresql%2Esql?download=true",
    );
  });

  it("calls visibility update endpoint with encoded object key", async () => {
    await updateObjectVisibility(settings, {
      bucket: "demo",
      objectKey: "docs/sample.postgresql.sql",
      visibility: "public",
    });

    expect(apiRequestMock).toHaveBeenCalledWith(
      settings,
      expect.objectContaining({
        method: "PATCH",
        url: "/api/v1/buckets/demo/objects/visibility/docs/sample%2Epostgresql%2Esql",
        data: { visibility: "public" },
      }),
    );
  });

  it("builds a multipart batch upload request for folders", async () => {
    const readme = new File(["hello"], "readme.txt", { type: "text/plain" });
    const logo = new File(["png"], "logo.png", { type: "image/png" });
    Object.defineProperty(readme, "webkitRelativePath", {
      configurable: true,
      value: "assets/readme.txt",
    });
    Object.defineProperty(logo, "webkitRelativePath", {
      configurable: true,
      value: "assets/images/logo.png",
    });

    await uploadFolder(settings, {
      bucket: "demo",
      prefix: "docs/",
      files: [readme, logo],
      visibility: "private",
    });

    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        method: "POST",
        timeout: 0,
        url: "/api/v1/buckets/demo/objects/batch",
        headers: expect.objectContaining({
          "X-Allow-Overwrite": "false",
        }),
      }),
    );

    const formData = request.mock.calls[0]?.[0]?.data as FormData;
    expect(formData.get("prefix")).toBe("docs/");
    expect(formData.get("visibility")).toBe("private");
    expect(formData.get("manifest")).toBe(
      JSON.stringify([
        { file_field: "file_0", relative_path: "assets/readme.txt" },
        { file_field: "file_1", relative_path: "assets/images/logo.png" },
      ]),
    );
    expect(formData.get("file_0")).toBeInstanceOf(File);
    expect((formData.get("file_0") as File).name).toBe("readme.txt");
    expect(formData.get("file_1")).toBeInstanceOf(File);
    expect((formData.get("file_1") as File).name).toBe("logo.png");
  });

  it("normalizes folder upload conflict responses into ApiError with code", async () => {
    request.mockRejectedValueOnce({
      isAxiosError: true,
      message: "Request failed with status code 409",
      response: {
        status: 409,
        data: {
          error: {
            code: "object_exists",
            message: "one or more objects already exist",
          },
        },
      },
    });

    const readme = new File(["hello"], "readme.txt", { type: "text/plain" });
    Object.defineProperty(readme, "webkitRelativePath", {
      configurable: true,
      value: "assets/readme.txt",
    });

    await expect(
      uploadFolder(settings, {
        bucket: "demo",
        prefix: "docs/",
        files: [readme],
        visibility: "private",
      }),
    ).rejects.toMatchObject({
      status: 409,
      code: "object_exists",
      message: "one or more objects already exist",
    });
  });

  it("passes recursive deletion to the folder endpoint when requested", async () => {
    await deleteFolder(settings, "demo", "docs/", { recursive: true });

    expect(apiRequestMock).toHaveBeenCalledWith(
      settings,
      expect.objectContaining({
        method: "DELETE",
        url: "/api/v1/buckets/demo/folders",
        params: {
          path: "docs/",
          recursive: true,
        },
      }),
    );
  });

  it("posts mixed explorer entries to the batch delete endpoint", async () => {
    await deleteExplorerEntriesBatch(settings, "demo", [
      { type: "file", path: "docs/readme.txt" },
      { type: "directory", path: "docs/assets/" },
    ]);

    expect(apiRequestMock).toHaveBeenCalledWith(
      settings,
      expect.objectContaining({
        method: "POST",
        url: "/api/v1/buckets/demo/entries/batch-delete",
        data: {
          items: [
            { type: "file", path: "docs/readme.txt" },
            { type: "directory", path: "docs/assets/" },
          ],
        },
      }),
    );
  });

  it("lists recycle bin items with cursor pagination params", async () => {
    await listRecycleBinObjects(settings, {
      bucket: "demo",
      limit: 20,
      cursor: "cursor-1",
    });

    expect(apiRequestMock).toHaveBeenCalledWith(
      settings,
      expect.objectContaining({
        method: "GET",
        url: "/api/v1/recycle-bin/objects",
        params: {
          bucket: "demo",
          limit: 20,
          cursor: "cursor-1",
        },
      }),
    );
  });

  it("posts recycle bin restore requests with item ids", async () => {
    await restoreRecycleBinObjects(settings, [12, 15]);

    expect(apiRequestMock).toHaveBeenCalledWith(
      settings,
      expect.objectContaining({
        method: "POST",
        url: "/api/v1/recycle-bin/objects/restore",
        data: {
          item_ids: [12, 15],
        },
      }),
    );
  });

  it("posts recycle bin permanent delete requests with item ids", async () => {
    await deleteRecycleBinObjects(settings, [7]);

    expect(apiRequestMock).toHaveBeenCalledWith(
      settings,
      expect.objectContaining({
        method: "POST",
        url: "/api/v1/recycle-bin/objects/batch-delete",
        data: {
          item_ids: [7],
        },
      }),
    );
  });

  it("downloads folder archives as blobs and honors the response filename", async () => {
    request.mockResolvedValue({
      data: new Blob(["zip-content"], { type: "application/zip" }),
      headers: {
        "content-disposition": `attachment; filename*=UTF-8''docs%20archive.zip`,
      },
    });

    await downloadFolderZip(settings, "demo", "docs/");

    expect(request).toHaveBeenCalledWith(
      expect.objectContaining({
        method: "GET",
        timeout: 0,
        url: "/api/v1/buckets/demo/folders/archive",
        params: {
          path: "docs/",
        },
        responseType: "blob",
      }),
    );
    expect(URL.createObjectURL).toHaveBeenCalled();
    expect(HTMLAnchorElement.prototype.click).toHaveBeenCalled();
    expect(URL.revokeObjectURL).toHaveBeenCalledWith("blob:folder-zip");
  });

  it("falls back to the folder name when archive headers are missing", async () => {
    const createdLinks: HTMLAnchorElement[] = [];
    const originalCreateElement = document.createElement.bind(document);
    const createElementSpy = vi.spyOn(document, "createElement");
    createElementSpy.mockImplementation(((
      tagName: string,
      options?: ElementCreationOptions,
    ) => {
      const element = originalCreateElement(tagName, options);
      if (tagName.toLowerCase() === "a") {
        createdLinks.push(element as HTMLAnchorElement);
      }
      return element;
    }) as typeof document.createElement);

    request.mockResolvedValue({
      data: new Blob(["zip-content"], { type: "application/zip" }),
      headers: {},
    });

    await downloadFolderZip(settings, "demo", "docs/nested/");

    expect(createdLinks[0]?.download).toBe("nested.zip");
    expect(HTMLAnchorElement.prototype.click).toHaveBeenCalled();

    createElementSpy.mockRestore();
  });
});
