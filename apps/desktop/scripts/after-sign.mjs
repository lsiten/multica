import identityHook from "../build/after-sign.cjs";
import verifyMacRelease from "./verify-mac-release.mjs";

export default async function afterSign(context) {
  await identityHook.default(context);
  verifyMacRelease(context);
}
