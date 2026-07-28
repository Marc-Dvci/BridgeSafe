import type { Metadata } from "next";
import "./globals.css";

export const metadata: Metadata = {
  title: "BridgeSafe — Private Treasury",
  description:
    "A Flare-controlled XRPL treasury: confidential payment instructions, policy enforced in a TEE, settlement proved by the Flare Data Connector.",
};

export default function RootLayout({ children }: { children: React.ReactNode }) {
  return (
    <html lang="en">
      <body>{children}</body>
    </html>
  );
}
