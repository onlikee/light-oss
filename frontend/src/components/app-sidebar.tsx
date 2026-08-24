"use client"

import * as React from "react"
import { useLocation } from "react-router-dom"

import { NavMain } from "@/components/nav-main"
import { NavProjects } from "@/components/nav-projects"
import { NavUser } from "@/components/nav-user"
import { TeamSwitcher } from "@/components/team-switcher"
import {
  Sidebar,
  SidebarContent,
  SidebarFooter,
  SidebarHeader,
  SidebarRail,
} from "@/components/ui/sidebar"
import {
  CompassIcon,
  GlobeIcon,
  HardDriveIcon,
  SettingsIcon,
} from "lucide-react"
import { getApiHostLabel, hasBearerToken } from "@/lib/connection"
import { useI18n } from "@/lib/i18n"
import { useAppSettings } from "@/lib/settings"

const lastBucketRouteStorageKey = "light-oss-last-bucket-route"

function isBucketRoute(route: string) {
  return (
    route === "/buckets" ||
    route.startsWith("/buckets/") ||
    route.startsWith("/buckets?")
  )
}

export function AppSidebar({ ...props }: React.ComponentProps<typeof Sidebar>) {
  const { pathname, search } = useLocation()
  const { settings } = useAppSettings()
  const { t } = useI18n()
  const currentBucketRoute =
    isBucketRoute(pathname) ? `${pathname}${search}` : null
  const [lastBucketRoute, setLastBucketRoute] = React.useState(() => {
    if (typeof window === "undefined") {
      return ""
    }

    const storedRoute = window.localStorage.getItem(lastBucketRouteStorageKey) ?? ""

    return isBucketRoute(storedRoute) ? storedRoute : ""
  })

  React.useEffect(() => {
    if (!currentBucketRoute) {
      return
    }

    setLastBucketRoute(currentBucketRoute)
    window.localStorage.setItem(lastBucketRouteStorageKey, currentBucketRoute)
  }, [currentBucketRoute])

  const bucketNavUrl = currentBucketRoute ?? (lastBucketRoute || "/buckets")

  const data = {
    workspace: {
      name: t("app.name"),
      logo: (
        <img
          alt={t("app.name")}
          className="size-8 object-contain"
          src="/LOGO.png"
        />
      ),
      meta: getApiHostLabel(settings.apiBaseUrl),
    },
    navMain: [
      {
        title: t("nav.dashboard"),
        url: "/dashboard",
        icon: <CompassIcon />,
        isActive: pathname === "/" || pathname.startsWith("/dashboard"),
      },
      {
        title: t("nav.buckets"),
        url: bucketNavUrl,
        icon: <HardDriveIcon />,
        isActive: pathname.startsWith("/buckets"),
      },
      {
        title: t("nav.sites"),
        url: "/sites",
        icon: <GlobeIcon />,
        isActive: pathname.startsWith("/sites"),
      },
      {
        title: t("nav.settings"),
        url: "/settings",
        icon: <SettingsIcon />,
        isActive: pathname.startsWith("/settings"),
      },
    ],
    connection: {
      host: getApiHostLabel(settings.apiBaseUrl),
      tokenConfigured: hasBearerToken(settings.bearerToken),
    },
  }

  return (
    <Sidebar collapsible="icon" {...props}>
      <SidebarHeader>
        <TeamSwitcher workspace={data.workspace} />
      </SidebarHeader>
      <SidebarContent>
        <NavMain items={data.navMain} />
        <NavProjects connection={data.connection} />
      </SidebarContent>
      <SidebarFooter>
        <NavUser />
      </SidebarFooter>
      <SidebarRail />
    </Sidebar>
  )
}
