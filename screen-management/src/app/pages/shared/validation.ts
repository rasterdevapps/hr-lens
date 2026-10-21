import { computed, signal } from '@angular/core';

/**
 * Field checks shared by every form. They mirror the server's rules (backend/internal/api) so a
 * mistake shows while typing; the server still checks everything again.
 */
export type Errors = Record<string, string>;

/** Size in UTF-8 bytes, which is what the server's limits count */
const bytes = (s: string) => new TextEncoder().encode(s).length;

export function requiredText(value: string, max: number, label = 'This field') {
  const v = value.trim();
  if (!v) return `${label} is required`;
  if (bytes(v) > max) return `Use at most ${max} characters`;
  return '';
}

const NAME = /^[\p{L}\p{M}][\p{L}\p{M} .'-]*$/u;
export function personName(value: string, label: string) {
  return requiredText(value, 100, label) ||
    (NAME.test(value.trim()) ? '' : 'Use letters, spaces, apostrophes, dots or hyphens only');
}

const CODE = /^[A-Za-z0-9][A-Za-z0-9._/-]*$/;
export function employeeCode(value: string) {
  return requiredText(value, 50, 'Employee ID') ||
    (CODE.test(value.trim()) ? '' : 'Use letters, numbers, dots, dashes, underscores or slashes, without spaces');
}

const EMAIL = /^[A-Za-z0-9.!#$%&'*+/=?^_`{|}~-]+@[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\.[A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?)+$/;
export function email(value: string) {
  const v = value.trim();
  if (!v) return 'Email is required';
  if (bytes(v) > 254) return 'Use at most 254 characters';
  if (/\s/.test(value.trim())) return 'Email cannot contain spaces';
  if (!v.includes('@')) return 'Email must contain @';
  return EMAIL.test(v) && !v.split('@')[0].startsWith('.') && !v.split('@')[0].endsWith('.') && !v.includes('..')
    ? '' : 'Enter a valid email address, such as name@company.com';
}

export const PASSWORD_MIN = 8;
export const PASSWORD_MAX_BYTES = 72;
export function password(value: string, label = 'Password') {
  if (!value) return `${label} is required`;
  if ([...value].length < PASSWORD_MIN) return `Use at least ${PASSWORD_MIN} characters (${[...value].length} so far)`;
  if (bytes(value) > PASSWORD_MAX_BYTES) return `Use at most ${PASSWORD_MAX_BYTES} characters`;
  return '';
}

export const MOBILE_MAX_DIGITS = 15;
/** Optional: numbers only, 7 to 15 digits */
export function mobile(value: string) {
  const v = value.trim();
  if (!v) return '';
  if (!/^[0-9]+$/.test(v)) return 'Use numbers only';
  return v.length < 7 || v.length > MOBILE_MAX_DIGITS ? `Enter 7 to ${MOBILE_MAX_DIGITS} digits (${v.length} so far)` : '';
}

export function timeZone(value: string) {
  const v = value.trim();
  if (!v) return '';   // the server uses Asia/Kolkata
  try {
    new Intl.DateTimeFormat('en', { timeZone: v });
    return /^[A-Za-z_]+(\/[A-Za-z0-9_+-]+)*$/.test(v) ? '' : 'Unknown time zone, use a name like Asia/Kolkata';
  } catch {
    return 'Unknown time zone, use a name like Asia/Kolkata';
  }
}

const DOMAIN = /^[a-z0-9]([a-z0-9-]*[a-z0-9])?(\.[a-z0-9]([a-z0-9-]*[a-z0-9])?)+$/;
/** Same clean-up as the server: "https://www.Example.com/path" counts as example.com */
export function cleanDomain(value: string) {
  let s = value.trim().toLowerCase();
  const scheme = s.indexOf('://');
  if (scheme >= 0) s = s.slice(scheme + 3);
  const end = s.search(/[/?#:]/);
  if (end >= 0) s = s.slice(0, end);
  return s.replace(/^www\./, '');
}
export function website(value: string) {
  if (!value.trim()) return 'Website is required';
  const d = cleanDomain(value);
  return DOMAIN.test(d) && d.length <= 253 ? '' : 'Enter a website such as example.com';
}

/**
 * Error state for one form. Checks run on every keystroke; a field shows its error once the person
 * has typed in it or left it, and every field shows after a save attempt. Errors from the server stay
 * until that field is changed.
 */
export class FormErrors {
  private readonly touched = signal<ReadonlySet<string>>(new Set());
  private readonly submitted = signal(false);
  private readonly local = signal<Errors>({});
  private readonly server = signal<Errors>({});

  readonly shown = computed<Errors>(() => {
    const out: Errors = {};
    const all = this.submitted(), touched = this.touched();
    for (const [k, v] of Object.entries(this.local())) if (v && (all || touched.has(k))) out[k] = v;
    for (const [k, v] of Object.entries(this.server())) if (v && !out[k]) out[k] = v;
    return out;
  });
  readonly any = computed(() => Object.keys(this.shown()).length > 0);

  constructor(private readonly check: () => Errors) {}

  /** Call on input: shows the field's error and drops the server's, which may no longer apply */
  touch(field: string) {
    this.touched.update(t => (t.has(field) ? t : new Set(t).add(field)));
    if (this.server()[field]) this.server.update(({ [field]: _, ...rest }) => rest);
    this.refresh();
  }

  /** Call on blur: shows the field's error without dropping one the server gave */
  blur(field: string) {
    this.touched.update(t => (t.has(field) ? t : new Set(t).add(field)));
    this.refresh();
  }

  /** Re-runs the checks, e.g. when another field this one depends on changed */
  refresh() { this.local.set(this.check()); }

  /** Shows every field's error; true when there is none */
  submit() {
    this.submitted.set(true);
    this.refresh();
    return !Object.values(this.local()).some(Boolean);
  }

  setServer(fields: Errors) { this.server.set({ ...fields }); }
  setServerField(field: string, message: string) {
    this.server.update(({ [field]: _, ...rest }) => (message ? { ...rest, [field]: message } : rest));
  }

  reset() {
    this.touched.set(new Set());
    this.submitted.set(false);
    this.local.set({});
    this.server.set({});
  }
}
