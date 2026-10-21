import { Component, computed, HostListener, inject, signal } from '@angular/core';
import { DatePipe } from '@angular/common';
import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { RouterLink } from '@angular/router';
import { MatIconModule } from '@angular/material/icon';
import { MatTooltipModule } from '@angular/material/tooltip';
import { firstValueFrom } from 'rxjs';
import { ApiError, Team, TeamStatus } from '../../core/api.types';
import { AuthService, errorMessage } from '../../core/auth.service';
import { TimeTrackerService } from '../dashboard/time-tracker.service';

interface Draft { id: number | null; name: string; }
import { ADMIN_TABS } from '../shared/admin-tabs';
import { FormErrors, requiredText } from '../shared/validation';
import { hash } from '../dashboard/time-tracker.service';

const TILE_COLORS = ['#4f46e5', '#0ea5e9', '#f97316', '#14b8a6', '#a855f7', '#ec4899', '#22c55e', '#64748b'];

@Component({
  selector: 'app-teams',
  standalone: true,
  imports: [DatePipe, RouterLink, MatIconModule, MatTooltipModule],
  templateUrl: './teams.component.html',
  styleUrl: './teams.component.scss',
})
export class TeamsComponent {
  private readonly http = inject(HttpClient);
  private readonly auth = inject(AuthService);
  private readonly people = inject(TimeTrackerService);

  /** Only a super admin can add, rename, archive or restore teams */
  readonly canManage = computed(() => this.auth.role() === 'super_admin');
  readonly tabs = ADMIN_TABS.filter(t => this.auth.can(t.route));

  tile(name: string) {
    return { letter: (name[0] ?? '?').toUpperCase(), color: TILE_COLORS[Math.abs(hash(name)) % TILE_COLORS.length] };
  }

  showing = signal<TeamStatus>('active');
  teams = signal<Team[]>([]);
  loading = signal(true);
  listError = signal('');
  notice = signal('');

  draft = signal<Draft | null>(null);
  /** Checks the team name as it is typed */
  readonly form = new FormErrors(() => ({ name: this.checkName() }));
  readonly nameError = computed(() => this.form.shown()['name'] ?? this.formError());
  private readonly formError = signal('');
  saving = signal(false);

  constructor() {
    this.load();
  }

  async load() {
    this.loading.set(true);
    try {
      this.teams.set(await firstValueFrom(this.http.get<Team[]>('/api/teams', { params: { status: this.showing() } })));
      this.listError.set('');
    } catch (err) {
      this.teams.set([]);
      this.listError.set(errorMessage(err, 'Could not load teams.'));
    } finally {
      this.loading.set(false);
    }
  }

  toggleShowing() {
    this.showing.update(s => (s === 'active' ? 'inactive' : 'active'));
    this.notice.set('');
    this.load();
  }

  // ---------- Add / rename ----------
  add() { this.open({ id: null, name: '' }); }
  edit(t: Team) { this.open({ id: t.id, name: t.name }); }

  private open(draft: Draft) {
    this.form.reset();
    this.formError.set('');
    this.draft.set(draft);
  }

  private checkName() {
    const d = this.draft();
    if (!d) return '';
    const name = d.name.trim();
    const error = requiredText(name, 100, 'Team name');
    if (error) return error;
    // Names are unique regardless of case; the server has the final say, including archived teams
    const clash = this.teams().some(t => t.id !== d.id && t.name.trim().toLowerCase() === name.toLowerCase());
    return clash ? 'A team with this name already exists' : '';
  }

  setName(d: Draft, value: string) {
    d.name = value;
    this.formError.set('');
    this.form.touch('name');
  }

  cancel() { this.draft.set(null); }

  async save() {
    const d = this.draft();
    if (!d || this.saving()) return;
    const name = d.name.trim();
    this.formError.set('');
    if (!this.form.submit()) return;
    this.saving.set(true);
    try {
      if (d.id === null) await firstValueFrom(this.http.post('/api/teams', { name }));
      else await firstValueFrom(this.http.put(`/api/teams/${d.id}`, { name }));
      this.draft.set(null);
      await this.afterChange(d.id === null ? `${name} added.` : 'Team renamed.');
    } catch (err) {
      const api = err instanceof HttpErrorResponse ? (err.error as ApiError | null) : null;
      if (api?.fields?.['name']) this.form.setServerField('name', api.fields['name']);
      else this.formError.set(errorMessage(err));
    } finally {
      this.saving.set(false);
    }
  }

  // ---------- Archive / restore ----------
  async setStatus(t: Team, status: TeamStatus) {
    try {
      await firstValueFrom(this.http.put(`/api/teams/${t.id}/status`, { status }));
      await this.afterChange(status === 'inactive'
        ? `${t.name} archived. Find it under "Show Inactive Teams".`
        : `${t.name} restored.`);
    } catch (err) {
      this.notice.set(errorMessage(err));
    }
  }

  private async afterChange(message: string) {
    this.notice.set(message);
    await this.load();
    // Teams feed the Employees / Teams panel on other pages
    const me = this.auth.user();
    if (me) this.people.loadPeople(me.id, true).catch(() => undefined);
  }

  @HostListener('document:keydown.escape')
  onEscape() { this.draft.set(null); }
}
