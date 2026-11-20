import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { cleanup, fireEvent, render, screen, within } from "@testing-library/react";
import { MemoryRouter } from "react-router";
import type { AppNotification } from "@/api/types";
import { notificationToast } from "@/hooks/queries/notifications";
import { NotificationInbox } from "./NotificationInbox";

const state = vi.hoisted(() => ({ markRead: vi.fn(), openPlugin: vi.fn() }));
vi.mock("@/lib/buildPluginHref", () => ({ navigateToPluginRoute: state.openPlugin }));
beforeEach(() => {
  state.markRead.mockReset();
  state.openPlugin.mockReset();
});
vi.mock("@/hooks/queries/notifications", async (importOriginal) => ({
  ...(await importOriginal<typeof import("@/hooks/queries/notifications")>()),
  useMarkNotificationsRead: () => ({ mutate: state.markRead, isPending: false }),
  useSetManyNotificationSeriesMuted: () => ({ mutate: vi.fn(), isPending: false }),
}));

afterEach(cleanup);

const notices = [
  {
    id: "notice-webhook",
    type: "webhook.auto_disabled",
    profile_id: "owner",
    reason_flags: {},
    created_at: new Date().toISOString(),
    read_at: null,
  },
  {
    id: "notice-destination",
    type: "plugin_destination.auto_disabled",
    profile_id: "owner",
    reason_flags: {},
    created_at: new Date(Date.now() - 60_000).toISOString(),
    read_at: null,
  },
] as unknown as AppNotification[];

it("shows a stopped webhook or destination with a link to notification settings", () => {
  const list = {
    isLoading: false,
    isError: false,
    hasNextPage: false,
    data: { pages: [{ notifications: notices }] },
  } as unknown as Parameters<typeof NotificationInbox>[0]["list"];
  render(
    <MemoryRouter>
      <NotificationInbox
        switcher={null}
        status="all"
        onStatusChange={() => {}}
        list={list}
        unreadCount={2}
        markAllRead={{ mutate: vi.fn(), isPending: false } as never}
        canMuteSeries={false}
      />
    </MemoryRouter>,
  );

  for (const title of ["A webhook stopped working", "A notification destination stopped working"]) {
    const link = screen.getByText(title).closest("a");
    expect(link?.getAttribute("href")).toBe("/settings/notifications");
    expect(within(link!).getByText("Open notification settings to fix it.")).toBeTruthy();
  }
});

it("toasts a stopped webhook or destination as the inbox titles it", () => {
  expect(notificationToast(notices[0]!)?.title).toBe("A webhook stopped working");
  expect(notificationToast(notices[1]!)?.title).toBe("A notification destination stopped working");
});

function showNotice(type: string, url: string) {
  const notice: AppNotification = {
    ...notices[0]!,
    id: "incoming-one",
    type,
    reason_flags: {
      title: "A <script>notice</script>",
      message: "A saved reminder",
      url,
    },
  };
  const list = {
    isLoading: false,
    isError: false,
    hasNextPage: false,
    data: { pages: [{ notifications: [notice] }] },
  } as unknown as Parameters<typeof NotificationInbox>[0]["list"];
  render(
    <MemoryRouter>
      <NotificationInbox
        switcher={null}
        status="all"
        onStatusChange={() => {}}
        list={list}
        unreadCount={1}
        markAllRead={{ mutate: vi.fn(), isPending: false } as never}
        canMuteSeries={false}
      />
    </MemoryRouter>,
  );
  return notice;
}

it.each(["external.notification", "episode.premiere"])(
  "renders %s as plain text with its local destination",
  (type) => {
    const notice = showNotice(type, "/notifications?source=integration");
    expect(screen.getByText("A <script>notice</script>")).toBeInTheDocument();
    expect(screen.getByText("A saved reminder")).toBeInTheDocument();
    expect(screen.getByRole("link", { name: /A <script>notice/ })).toHaveAttribute(
      "href",
      "/notifications?source=integration",
    );
    expect(document.querySelector("script")).toBeNull();
    expect(notificationToast(notice)).toEqual({
      title: "A <script>notice</script>",
      description: "A saved reminder",
    });
  },
);

it.each([
  "https://example.test",
  "//example.test",
  "/%2f%2fexample.test",
  "/%5cexample.test",
  "javascript:alert(1)",
])("leaves the unsafe destination %s unlinked", (url) => {
  showNotice("external.notification", url);
  expect(screen.getByText("A <script>notice</script>")).toBeInTheDocument();
  expect(screen.queryByRole("link", { name: /A <script>notice/ })).not.toBeInTheDocument();
});

it("opens an incoming plugin destination through the authenticated launch flow", () => {
  const url = "/api/v2/plugin-content/plugins/1/app#/details";
  showNotice("external.notification", url);
  fireEvent.click(screen.getByRole("link", { name: /A <script>notice/ }));
  expect(state.openPlugin).toHaveBeenCalledWith(url);
  expect(state.markRead).toHaveBeenCalledWith(["incoming-one"]);
});
