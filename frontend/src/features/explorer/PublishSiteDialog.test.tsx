import { screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { vi } from "vitest";
import { PublishSiteDialog } from "./PublishSiteDialog";
import { renderWithApp } from "../../test/test-utils";

describe("PublishSiteDialog", () => {
  beforeEach(() => {
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

  it("keeps the typed domains when used without a bucket list", async () => {
    const onSubmit = vi.fn().mockResolvedValue(undefined);

    renderWithApp(
      <PublishSiteDialog
        bucket="websites"
        onSubmit={onSubmit}
        pending={false}
        prefix="demo/"
      />,
    );

    await userEvent.click(
      screen.getByRole("button", { name: "Publish site" }),
    );

    const dialog = await screen.findByRole("dialog");
    const domainsInput = within(dialog).getByLabelText("Domains");

    await userEvent.type(domainsInput, "demo.localhost");

    expect(domainsInput).toHaveValue("demo.localhost");
  });
});
