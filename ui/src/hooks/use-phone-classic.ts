import { useClassic } from "@/hooks/use-classic";
import { useMedia } from "@/hooks/use-media";

/** Below md the dashboard is a phone's. */
export const PHONE_QUERY = "(max-width: 767px)";

/** The classic UI profile on a phone: be the Python dashboard's mobile app (lib/phone.ts). With
 * the profile off, a phone shows the default dashboard exactly as before. */
export function usePhoneClassic(): boolean {
  const classic = useClassic();
  const phone = useMedia(PHONE_QUERY);
  return classic && phone;
}
