import { screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { vi } from "vitest";
import { SitesPage } from "./SitesPage";
import { renderWithApp } from "../test/test-utils";

vi.mock("../api/sites", () => ({
  listSites: vi.fn(),
  createSite: vi.fn(),
  updateSite: vi.fn(),
  deleteSite: vi.fn(),
  uploadFileAndPublishSite: vi.fn(),
  uploadAndPublishSite: vi.fn(),
}));

vi.mock("../api/buckets", () => ({
  listBuckets: vi.fn(),
}));

import { listBuckets } from "../api/buckets";
import {
  createSite,
  deleteSite,
  listSites,
  updateSite,
  uploadFileAndPublishSite,
  uploadAndPublishSite,
} from "../api/sites";

const defaultBuckets = {
  items: [
    {
      id: 1,
      name: "websites",
      created_at: "2026-03-30T00:00:00Z",
      updated_at: "2026-03-30T00:00:00Z",
    },
    {
      id: 2,
      name: "marketing",
      created_at: "2026-03-30T00:00:00Z",
      updated_at: "2026-03-30T00:00:00Z",
    },
  ],
};

const existingSite = {
  id: 7,
  bucket: "websites",
  root_prefix: "app/",
  enabled: true,
  index_document: "index.html",
  error_document: "",
  spa_fallback: true,
  domains: ["demo.localhost"],
  created_at: "2026-03-30T00:00:00Z",
  updated_at: "2026-03-30T00:00:00Z",
};

describe("SitesPage", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    Object.defineProperty(Element.prototype, "hasPointerCapture", {
      configurable: true,
      value: vi.fn(() => false),
    });
    Object.defineProperty(Element.prototype, "setPointerCapture", {
      configurable: true,
      value: vi.fn(),
    });
    Object.defineProperty(Element.prototype, "releasePointerCapture", {
      configurable: true,
      value: vi.fn(),
    });
    Object.defineProperty(HTMLElement.prototype, "scrollIntoView", {
      configurable: true,
      value: vi.fn(),
    });
  });

  it("loads and renders the site list", async () => {
    vi.mocked(listSites).mockResolvedValue({
      items: [existingSite],
    });
    vi.mocked(listBuckets).mockResolvedValue(defaultBuckets);

    renderWithApp(<SitesPage />);

    expect(await screen.findByText("Site list")).toBeInTheDocument();
    expect(await screen.findByText("app/")).toBeInTheDocument();
    expect(
      screen.getByText((content) => content.includes("demo.localhost")),
    ).toBeInTheDocument();
    expect(screen.getByText("websites")).toBeInTheDocument();
    expect(screen.getByText("Index document")).toBeInTheDocument();
  });

  it("creates a site from the page dialog with the full payload", async () => {
    vi.mocked(listSites)
      .mockResolvedValueOnce({ items: [] })
      .mockResolvedValueOnce({ items: [] });
    vi.mocked(listBuckets).mockResolvedValue(defaultBuckets);
    vi.mocked(createSite).mockResolvedValue({
      ...existingSite,
      id: 11,
      bucket: "websites",
      root_prefix: "landing/",
      enabled: false,
      index_document: "home.html",
      error_document: "404.html",
      spa_fallback: false,
      domains: ["landing.localhost", "www.localhost"],
    });

    renderWithApp(<SitesPage />);

    await userEvent.click(
      await screen.findByRole("button", { name: "Create site" }),
    );

    const dialog = await screen.findByRole("dialog");
    await userEvent.clear(within(dialog).getByLabelText("Root prefix"));
    await userEvent.type(
      within(dialog).getByLabelText("Root prefix"),
      "landing/",
    );
    await userEvent.type(
      within(dialog).getByLabelText("Domains"),
      "landing.localhost, www.localhost",
    );
    await userEvent.click(
      within(dialog).getByRole("combobox", { name: "Enabled" }),
    );
    await userEvent.click(
      await screen.findByRole("option", { name: "Disabled" }),
    );
    await userEvent.clear(within(dialog).getByLabelText("Index document"));
    await userEvent.type(
      within(dialog).getByLabelText("Index document"),
      "home.html",
    );
    await userEvent.type(
      within(dialog).getByLabelText("Error document"),
      "404.html",
    );
    await userEvent.click(
      within(dialog).getByRole("combobox", { name: "SPA fallback" }),
    );
    await userEvent.click(
      await screen.findByRole("option", { name: "Disabled" }),
    );
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Create site" }),
    );

    await waitFor(() => {
      expect(createSite).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        {
          bucket: "websites",
          root_prefix: "landing/",
          enabled: false,
          index_document: "home.html",
          error_document: "404.html",
          spa_fallback: false,
          domains: ["landing.localhost", "www.localhost"],
        },
      );
    });

    expect(await screen.findByText("Site created")).toBeInTheDocument();
  }, 15000);

  it("uploads a folder and publishes a site from the page header", async () => {
    vi.mocked(listSites)
      .mockResolvedValueOnce({ items: [] })
      .mockResolvedValueOnce({ items: [existingSite] });
    vi.mocked(listBuckets).mockResolvedValue(defaultBuckets);
    vi.mocked(uploadAndPublishSite).mockResolvedValue({
      uploaded_count: 2,
      site: existingSite,
    });

    renderWithApp(<SitesPage />);

    await userEvent.click(
      await screen.findByRole("button", { name: "Upload and publish" }),
    );

    const dialog = await screen.findByRole("dialog");
    await userEvent.type(
      within(dialog).getByLabelText("Parent prefix"),
      "deployments/",
    );

    const indexFile = new File(["<html>home</html>"], "index.html", {
      type: "text/html",
    });
    const appFile = new File(["console.log('demo')"], "app.js", {
      type: "application/javascript",
    });
    Object.defineProperty(indexFile, "webkitRelativePath", {
      configurable: true,
      value: "dist/index.html",
    });
    Object.defineProperty(appFile, "webkitRelativePath", {
      configurable: true,
      value: "dist/assets/app.js",
    });

    await userEvent.upload(within(dialog).getByLabelText("Folder"), [
      indexFile,
      appFile,
    ]);
    expect(within(dialog).getByText("deployments/dist/")).toBeInTheDocument();
    await userEvent.type(
      within(dialog).getByLabelText("Domains"),
      "demo.localhost",
    );
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Upload and publish" }),
    );

    await waitFor(() => {
      expect(uploadAndPublishSite).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        {
          bucket: "websites",
          parentPrefix: "deployments/",
          files: [indexFile, appFile],
          domains: ["demo.localhost"],
          enabled: true,
          indexDocument: "index.html",
          errorDocument: "",
          spaFallback: true,
          onProgress: expect.any(Function),
        },
      );
    });

    expect(await screen.findByText("Site published")).toBeInTheDocument();
  }, 15000);

  it("uploads a file and publishes a site from the page header", async () => {
    vi.mocked(listSites)
      .mockResolvedValueOnce({ items: [] })
      .mockResolvedValueOnce({ items: [existingSite] });
    vi.mocked(listBuckets).mockResolvedValue(defaultBuckets);
    vi.mocked(uploadFileAndPublishSite).mockResolvedValue(existingSite);

    renderWithApp(<SitesPage />);

    await userEvent.click(
      await screen.findByRole("button", { name: "Upload and publish" }),
    );

    const dialog = await screen.findByRole("dialog");
    await userEvent.click(
      within(dialog).getByRole("tab", { name: "Upload file and publish" }),
    );
    await userEvent.type(
      within(dialog).getByLabelText("Parent prefix"),
      "deployments/",
    );

    const landingFile = new File(["<html>home</html>"], "landing.html", {
      type: "text/html",
    });
    await userEvent.upload(within(dialog).getByLabelText("File"), landingFile);
    expect(within(dialog).getByText("deployments/")).toBeInTheDocument();
    expect(within(dialog).getByText("landing.html")).toBeInTheDocument();
    await userEvent.type(
      within(dialog).getByLabelText("Domains"),
      "demo.localhost",
    );
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Upload and publish" }),
    );

    await waitFor(() => {
      expect(uploadFileAndPublishSite).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        {
          bucket: "websites",
          parentPrefix: "deployments/",
          file: landingFile,
          domains: ["demo.localhost"],
          enabled: true,
          errorDocument: "",
          spaFallback: true,
          onProgress: expect.any(Function),
        },
      );
    });

    expect(await screen.findByText("Site published")).toBeInTheDocument();
  }, 15000);

  it("prefills the edit dialog and updates a site", async () => {
    vi.mocked(listSites)
      .mockResolvedValueOnce({ items: [existingSite] })
      .mockResolvedValueOnce({
        items: [
          {
            ...existingSite,
            root_prefix: "app-v2/",
            domains: ["app.localhost", "www.localhost"],
            enabled: false,
            spa_fallback: false,
          },
        ],
      });
    vi.mocked(listBuckets).mockResolvedValue(defaultBuckets);
    vi.mocked(updateSite).mockResolvedValue({
      ...existingSite,
      root_prefix: "app-v2/",
      domains: ["app.localhost", "www.localhost"],
      enabled: false,
      spa_fallback: false,
    });

    renderWithApp(<SitesPage />);

    await userEvent.click(await screen.findByRole("button", { name: "Edit" }));

    const dialog = await screen.findByRole("dialog");
    expect(within(dialog).getByDisplayValue("app/")).toBeInTheDocument();
    expect(
      within(dialog).getByDisplayValue("demo.localhost"),
    ).toBeInTheDocument();

    await userEvent.clear(within(dialog).getByLabelText("Root prefix"));
    await userEvent.type(
      within(dialog).getByLabelText("Root prefix"),
      "app-v2/",
    );
    await userEvent.clear(within(dialog).getByLabelText("Domains"));
    await userEvent.type(
      within(dialog).getByLabelText("Domains"),
      "app.localhost, www.localhost",
    );
    await userEvent.click(
      within(dialog).getByRole("combobox", { name: "Enabled" }),
    );
    await userEvent.click(
      await screen.findByRole("option", { name: "Disabled" }),
    );
    await userEvent.click(
      within(dialog).getByRole("combobox", { name: "SPA fallback" }),
    );
    await userEvent.click(
      await screen.findByRole("option", { name: "Disabled" }),
    );
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Save changes" }),
    );

    await waitFor(() => {
      expect(updateSite).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        7,
        {
          bucket: "websites",
          root_prefix: "app-v2/",
          enabled: false,
          index_document: "index.html",
          error_document: "",
          spa_fallback: false,
          domains: ["app.localhost", "www.localhost"],
        },
      );
    });

    expect(await screen.findByText("Site updated")).toBeInTheDocument();
  }, 15000);

  it("deletes a site after confirmation and refreshes the list", async () => {
    vi.mocked(listSites)
      .mockResolvedValueOnce({ items: [existingSite] })
      .mockResolvedValueOnce({ items: [] });
    vi.mocked(listBuckets).mockResolvedValue(defaultBuckets);
    vi.mocked(deleteSite).mockResolvedValue(undefined);

    renderWithApp(<SitesPage />);

    await userEvent.click(
      await screen.findByRole("button", { name: "Delete" }),
    );

    const dialog = await screen.findByRole("alertdialog");
    expect(within(dialog).getByText("Delete site?")).toBeInTheDocument();
    await userEvent.click(
      within(dialog).getByRole("button", { name: "Delete" }),
    );

    await waitFor(() => {
      expect(deleteSite).toHaveBeenCalledWith(
        { apiBaseUrl: "http://localhost:8080", bearerToken: "dev-token" },
        7,
      );
    });

    expect(await screen.findByText("Site deleted")).toBeInTheDocument();
  });

  it("shows an alert when loading the site list fails", async () => {
    vi.mocked(listSites).mockRejectedValue(new Error("load failed"));
    vi.mocked(listBuckets).mockResolvedValue(defaultBuckets);

    renderWithApp(<SitesPage />);

    expect(await screen.findByText("Failed to load sites")).toBeInTheDocument();
    expect(screen.getByText("load failed")).toBeInTheDocument();
  });
});
