import { Component, HostListener, inject, signal } from '@angular/core';
import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { firstValueFrom } from 'rxjs';
import { ApiError, Shift } from '../../core/api.types';
import { AuthService, errorMessage } from '../../core/auth.service';
import { ADMIN_TABS } from '../shared/admin-tabs';
import { FormErrors, requiredText } from '../shared/validation';

interface Draft { id: number | null; name: string; start: string; grace: string; }

const CLOCK = /^([01][0-9]|2[0-3]):[0-5][0-9]$/;

/** "HH:MM" → "h:mm AM" */
export function clock12(hhmm: string) {
  const [h, m] = hhmm.split(':').map(Number);
  return `${h % 12 || 12}:${String(m).padStart(2, '0')} ${h < 12 ? 'AM' : 'PM'}`;
}

/** start + minutes, as "HH:MM" */
export function addMinutes(start: string, minutes: number) {
  const [h, m] = start.split(':').map(Number);
  const t = (h * 60 + m + minutes) % 1440;
  return `${String(Math.floor(t / 60)).padStart(2, '0')}:${String(t % 60).padStart(2, '0')}`;
}

@Component({
  selector: 'app-shifts',
  standalone: true,
  imports: [RouterLink, MatIconModule, MatTooltipModule],
  templateUrl: './shifts.component.html',
  styleUrls: ['../teams/teams.component.scss', './shifts.component.scss'],
})
export class ShiftsComponent {
  private readonly http = inject(HttpClient);
  private readonly auth = inject(AuthService);

  readonly tabs = ADMIN_TABS.filter(t => this.auth.can(t.route));
  readonly clock12 = clock12;
  readonly addMinutes = addMinutes;

  shifts = signal<Shift[]>([]);
  loading = signal(true);
  listError = signal('');
  notice = signal('');

  draft = signal<Draft | null>(null);
  /** Same rules as the server (backend/internal/api/shifts.go) */
  readonly form = new FormErrors(() => this.check());
  readonly formError = signal('');
  saving = signal(false);
  /** Shift waiting for a second click on Delete */
  confirmDelete = signal<number | null>(null);

  constructor() {
    this.load();
  }

  async load() {
    this.loading.set(true);
    try {
      this.shifts.set(await firstValueFrom(this.http.get<Shift[]>('/api/shifts')));
      this.listError.set('');
    } catch (err) {
      this.shifts.set([]);
      this.listError.set(errorMessage(err, 'Could not load shifts.'));
    } finally {
      this.loading.set(false);
    }
  }

  // ---------- Add / edit ----------
  add() { this.open({ id: null, name: '', start: '10:00', grace: '0' }); }
  edit(s: Shift) { this.open({ id: s.id, name: s.name, start: s.start, grace: String(s.graceMinutes) }); }

  private open(draft: Draft) {
    this.form.reset();
    this.formError.set('');
    this.confirmDelete.set(null);
    this.draft.set(draft);
  }

  private grace(d: Draft) {
    const g = Number(d.grace);
    return d.grace.trim() !== '' && Number.isInteger(g) && g >= 0 && g <= 240 ? g : null;
  }

  private check(): Record<string, string> {
    const d = this.draft();
    if (!d) return {};
    const name = d.name.trim();
    let nameError = requiredText(name, 100, 'Shift name');
    if (!nameError && this.shifts().some(s => s.id !== d.id && s.name.trim().toLowerCase() === name.toLowerCase())) {
      nameError = 'A shift with this name already exists';
    }
    return {
      name: nameError,
      start: CLOCK.test(d.start) ? '' : 'Enter a start time',
      graceMinutes: this.grace(d) === null ? 'Use 0 to 240 minutes' : '',
    };
  }

  set(d: Draft, field: 'name' | 'start' | 'grace', value: string) {
    d[field] = value;
    this.formError.set('');
    this.form.touch(field === 'grace' ? 'graceMinutes' : field);
  }

  /** e.g. "On time until 10:09 AM, Late from 10:10 AM" */
  preview(d: Draft) {
    const grace = this.grace(d);
    if (!CLOCK.test(d.start) || grace === null) return '';
    return `On time until ${clock12(addMinutes(d.start, grace))}, Late from ${clock12(addMinutes(d.start, grace + 1))}`;
  }

  cancel() { this.draft.set(null); }

  async save() {
    const d = this.draft();
    if (!d || this.saving()) return;
    this.formError.set('');
    if (!this.form.submit()) return;
    const body = { name: d.name.trim(), start: d.start, graceMinutes: Number(d.grace) };
    this.saving.set(true);
    try {
      if (d.id === null) await firstValueFrom(this.http.post('/api/shifts', body));
      else await firstValueFrom(this.http.put(`/api/shifts/${d.id}`, body));
      this.draft.set(null);
      this.notice.set(d.id === null ? `${body.name} added. Assign it to people in Users.` : 'Shift saved.');
      await this.load();
    } catch (err) {
      const api = err instanceof HttpErrorResponse ? (err.error as ApiError | null) : null;
      if (api?.fields && Object.keys(api.fields).length) this.form.setServer(api.fields);
      else this.formError.set(errorMessage(err));
    } finally {
      this.saving.set(false);
    }
  }

  // ---------- Delete (asks once more first) ----------
  async remove(s: Shift) {
    if (this.confirmDelete() !== s.id) {
      this.confirmDelete.set(s.id);
      return;
    }
    this.confirmDelete.set(null);
    try {
      await firstValueFrom(this.http.delete(`/api/shifts/${s.id}`));
      this.notice.set(`${s.name} deleted. Its people now use the default shift.`);
      await this.load();
    } catch (err) {
      this.notice.set(errorMessage(err));
    }
  }

  @HostListener('document:keydown.escape')
  onEscape() {
    this.draft.set(null);
    this.confirmDelete.set(null);
  }
}
