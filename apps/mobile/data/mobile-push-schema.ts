import { z } from "zod";

export const MobilePushConfigurationSchema = z.object({
  enabled: z.boolean().default(false),
  topics: z.array(z.string()).default([]),
  environments: z.array(z.string()).default([]),
});
export type MobilePushConfiguration = z.infer<typeof MobilePushConfigurationSchema>;
export const EMPTY_MOBILE_PUSH_CONFIGURATION: MobilePushConfiguration = {
  enabled: false, topics: [], environments: [],
};
export const MobilePushRegistrationSchema = z.object({ registered: z.boolean() });
export interface MobilePushDevice {
  readonly token: string;
  readonly topic: string;
  readonly environment: "production" | "sandbox";
}
