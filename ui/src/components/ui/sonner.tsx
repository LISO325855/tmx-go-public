import {
  CircleCheckIcon,
  InfoIcon,
  Loader2Icon,
  OctagonXIcon,
  TriangleAlertIcon,
} from "lucide-react"
import { usePhoneClassic } from "@/hooks/use-phone-classic"
import { useTheme } from "@/lib/theme"
import { Toaster as Sonner, type ToasterProps } from "sonner"

const Toaster = ({ ...props }: ToasterProps) => {
  const { resolved } = useTheme()
  const phone = usePhoneClassic()
  // Tab bar: 59px links + 7px top + border + bottom safe area, with a 16px gap.
  const phoneOffset = phone ? { bottom: "calc(83px + max(9px, env(safe-area-inset-bottom)))", left: 16, right: 16 } : undefined

  return (
    <Sonner
      theme={resolved}
      className="toaster group"
      offset={phoneOffset}
      mobileOffset={phoneOffset}
      icons={{
        success: <CircleCheckIcon className="size-4" />,
        info: <InfoIcon className="size-4" />,
        warning: <TriangleAlertIcon className="size-4" />,
        error: <OctagonXIcon className="size-4" />,
        loading: <Loader2Icon className="size-4 animate-spin" />,
      }}
      style={
        {
          "--normal-bg": "var(--popover)",
          "--normal-text": "var(--popover-foreground)",
          "--normal-border": "var(--border)",
          "--border-radius": "var(--radius)",
        } as React.CSSProperties
      }
      {...props}
    />
  )
}

export { Toaster }
