import { screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { vi } from "vitest";
import { renderWithApp } from "../test/test-utils";
import { AppRoutes } from "./AppRoutes";

vi.mock("../components/Layout", async () => {
  const { Link, Outlet } =
    await vi.importActual<typeof import("react-router-dom")>(
      "react-router-dom",
    );

  return {
    Layout: () => (
      <>
        <nav>
          <Link to="/settings">Settings</Link>
          <Link to="/buckets/demo?prefix=dist%2Fassets%2F&limit=200">
            Cached bucket
          </Link>
        </nav>
        <Outlet />
      </>
    ),
  };
});

vi.mock("../pages/BucketObjectsPage", async () => {
  const React = await vi.importActual<typeof import("react")>("react");

  return {
    BucketObjectsPage: () => {
      const [value, setValue] = React.useState("");

      return (
        <label>
          Bucket state
          <input
            aria-label="Bucket state"
            onChange={(event) => setValue(event.target.value)}
            value={value}
          />
        </label>
      );
    },
  };
});

vi.mock("../pages/DashboardPage", () => ({
  DashboardPage: () => <div>Dashboard body</div>,
}));

vi.mock("../pages/BucketsPage", () => ({
  BucketsPage: () => <div>Buckets body</div>,
}));

vi.mock("../pages/SitesPage", () => ({
  SitesPage: () => <div>Sites body</div>,
}));

vi.mock("../pages/DocsPage", () => ({
  DocsPage: () => <div>Docs body</div>,
}));

vi.mock("../pages/SettingsPage", () => ({
  SettingsPage: () => <div>Settings body</div>,
}));

describe("AppRoutes", () => {
  it("keeps bucket detail state when leaving and returning to the route", async () => {
    const user = userEvent.setup();

    renderWithApp(<AppRoutes />, {
      route: "/buckets/demo?prefix=dist%2Fassets%2F&limit=200",
    });

    await user.type(screen.getByLabelText("Bucket state"), "kept");
    await user.click(screen.getByRole("link", { name: "Settings" }));

    expect(screen.getByText("Settings body")).toBeInTheDocument();

    await user.click(screen.getByRole("link", { name: "Cached bucket" }));

    expect(screen.getByLabelText("Bucket state")).toHaveValue("kept");
  });
});
