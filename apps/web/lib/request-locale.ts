import { cache } from "react";
import { cookies, headers } from "next/headers";
import { LOCALE_COOKIE, type SupportedLocale } from "@multica/core/i18n";
import {
  isSupportedLocale,
  MULTICA_LOCALE_HEADER,
  resolveLocaleFromSignals,
} from "./locale-routing";

export const MULTICA_SURFACE_HEADER = "x-multica-surface";

/** Public marketing routes use their own small dictionary and do not need the
 * full authenticated application locale catalog in the root layout payload. */
export function isLandingSurfacePath(pathname: string): boolean {
  return (
    pathname === "/" ||
    pathname === "/about" ||
    pathname === "/changelog" ||
    pathname === "/contact-sales" ||
    pathname === "/download" ||
    pathname === "/homepage" ||
    pathname === "/licensing" ||
    pathname === "/privacy" ||
    pathname === "/usecases" ||
    pathname.startsWith("/usecases/")
  );
}

export const getRequestLocale = cache(
  async (): Promise<SupportedLocale> => {
    const headerList = await headers();
    const headerLocale = headerList.get(MULTICA_LOCALE_HEADER);
    if (isSupportedLocale(headerLocale)) return headerLocale;

    const cookieStore = await cookies();
    return resolveLocaleFromSignals({
      cookieLocale: cookieStore.get(LOCALE_COOKIE)?.value,
      acceptLanguage: headerList.get("accept-language"),
    });
  },
);
