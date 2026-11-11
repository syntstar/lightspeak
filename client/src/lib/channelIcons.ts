// Selectable channel icons. The keys must match the server's
// allowlist (channelrules.go channelIcons); "" is the default (# / speaker).
import type { Component } from "svelte";
import Hash from "@lucide/svelte/icons/hash";
import Volume2 from "@lucide/svelte/icons/volume-2";
import Megaphone from "@lucide/svelte/icons/megaphone";
import MessageCircle from "@lucide/svelte/icons/message-circle";
import BookOpen from "@lucide/svelte/icons/book-open";
import Code from "@lucide/svelte/icons/code";
import Image from "@lucide/svelte/icons/image";
import Link from "@lucide/svelte/icons/link";
import Lightbulb from "@lucide/svelte/icons/lightbulb";
import Calendar from "@lucide/svelte/icons/calendar";
import Star from "@lucide/svelte/icons/star";
import Info from "@lucide/svelte/icons/info";
import Gamepad2 from "@lucide/svelte/icons/gamepad-2";
import Music from "@lucide/svelte/icons/music";
import Shield from "@lucide/svelte/icons/shield";
import Coffee from "@lucide/svelte/icons/coffee";
import Bot from "@lucide/svelte/icons/bot";
import Newspaper from "@lucide/svelte/icons/newspaper";
import Mic from "@lucide/svelte/icons/mic";
import Headphones from "@lucide/svelte/icons/headphones";
import Radio from "@lucide/svelte/icons/radio";
import Users from "@lucide/svelte/icons/users";
import Monitor from "@lucide/svelte/icons/monitor";
import Tv from "@lucide/svelte/icons/tv";
import Briefcase from "@lucide/svelte/icons/briefcase";
import PartyPopper from "@lucide/svelte/icons/party-popper";
import Film from "@lucide/svelte/icons/film";

// eslint-disable-next-line @typescript-eslint/no-explicit-any
type Icon = Component<any>;

const ICONS: Record<string, Icon> = {
  megaphone: Megaphone,
  "message-circle": MessageCircle,
  "book-open": BookOpen,
  code: Code,
  image: Image,
  link: Link,
  lightbulb: Lightbulb,
  calendar: Calendar,
  star: Star,
  info: Info,
  "gamepad-2": Gamepad2,
  music: Music,
  shield: Shield,
  coffee: Coffee,
  bot: Bot,
  newspaper: Newspaper,
  mic: Mic,
  headphones: Headphones,
  radio: Radio,
  users: Users,
  monitor: Monitor,
  tv: Tv,
  briefcase: Briefcase,
  "party-popper": PartyPopper,
  film: Film,
};

/** Choices per kind; the first ("") is the default icon. */
export const ICON_CHOICES: Record<"text" | "voice", string[]> = {
  text: [
    "",
    "megaphone",
    "message-circle",
    "book-open",
    "code",
    "image",
    "link",
    "lightbulb",
    "calendar",
    "star",
    "info",
    "gamepad-2",
    "music",
    "shield",
    "coffee",
    "bot",
    "newspaper",
  ],
  voice: [
    "",
    "mic",
    "headphones",
    "gamepad-2",
    "music",
    "radio",
    "coffee",
    "users",
    "monitor",
    "tv",
    "star",
    "briefcase",
    "book-open",
    "code",
    "party-popper",
    "film",
    "bot",
  ],
};

/** The icon component for a key of the given kind (default for unknown). */
export function channelIcon(kind: "text" | "voice", key: string | undefined): Icon {
  if (key && ICON_CHOICES[kind].includes(key)) return ICONS[key]!;
  return kind === "voice" ? Volume2 : Hash;
}
