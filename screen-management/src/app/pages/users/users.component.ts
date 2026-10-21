import { Component, computed, HostListener, inject, signal } from '@angular/core';
import { HttpClient, HttpErrorResponse } from '@angular/common/http';
import { takeUntilDestroyed, toObservable } from '@angular/core/rxjs-interop';
import { RouterLink } from '@angular/router';
import { MatIconModule } from '@angular/material/icon';
import { MatMenuModule } from '@angular/material/menu';
import { MatPaginatorModule, PageEvent } from '@angular/material/paginator';
import { MatTooltipModule } from '@angular/material/tooltip';
import { catchError, debounceTime, firstValueFrom, of, switchMap, tap } from 'rxjs';
import { ApiError, ApiUser, Lookups, UserInput, UserList, UserStatus } from '../../core/api.types';
import { AuthService, errorMessage, ROLES } from '../../core/auth.service';
import { ADMIN_TABS } from '../shared/admin-tabs';
import * as check from '../shared/validation';
import { Errors, FormErrors } from '../shared/validation';
import { TimeTrackerService } from '../dashboard/time-tracker.service';

type StatusFilter = UserStatus | 'all';
type SortKey = 'id' | 'name' | 'username' | 'role' | 'manager' | 'team' | 'lastActive';

interface Draft extends UserInput { id: number | null; password: string; }

const NO_LOOKUPS: Lookups = { roles: ROLES, teams: [], departments: [], branches: [], managers: [], shifts: [] };

const AVATAR_COLORS = ['#4f46e5', '#0ea5e9', '#f97316', '#14b8a6', '#a855f7', '#ec4899', '#22c55e', '#64748b'];

function ago(iso: string | null, now: number) {
  if (!iso) return 'Never';
  const s = Math.max(1, Math.round((now - new Date(iso).getTime()) / 1000));
  const units: [number, string][] = [[86400, 'day'], [3600, 'hour'], [60, 'minute'], [1, 'second']];
  const [size, name] = units.find(([u]) => s >= u)!;
  const n = Math.floor(s / size);
  return `${n} ${name}${n > 1 ? 's' : ''} ago`;
}

@Component({
  selector: 'app-users',
  standalone: true,
  imports: [RouterLink, MatIconModule, MatMenuModule, MatPaginatorModule, MatTooltipModule],
  templateUrl: './users.component.html',
  styleUrl: './users.component.scss',
})
export class UsersComponent {
  private readonly http = inject(HttpClient);
  private readonly auth = inject(AuthService);
  private readonly people = inject(TimeTrackerService);

  readonly tabs = ADMIN_TABS.filter(t => this.auth.can(t.route));

  readonly statuses: { value: StatusFilter; label: string }[] = [
    { value: 'active', label: 'Active' }, { value: 'inactive', label: 'Inactive' }, { value: 'all', label: 'All' },
  ];
  readonly columns: { key: SortKey; label: string }[] = [
    { key: 'id', label: 'Id' },
    { key: 'name', label: 'Name / Username' },
    { key: 'role', label: 'Role' },
    { key: 'manager', label: 'Manager' },
    { key: 'team', label: 'Teams' },
  ];

  // ---------- List ----------
  status = signal<StatusFilter>('active');
  search = signal('');
  sortKey = signal<SortKey>('name');
  ascending = signal(true);
  pageSize = signal(100);
  pageIndex = signal(0);
  private refreshes = signal(0);

  list = signal<UserList>({ items: [], total: 0, page: 1, pageSize: 100 });
  loading = signal(true);
  listError = signal('');
  now = signal(Date.now());

  private query = computed(() => ({
    status: this.status(), search: this.search().trim(), sort: this.sortKey(), dir: this.ascending() ? 'asc' : 'desc',
    page: this.pageIndex() + 1, pageSize: this.pageSize(), refresh: this.refreshes(),
  }));

  statusLabel = computed(() => this.statuses.find(s => s.value === this.status())!.label);
  rows = computed(() => this.list().items.map(user => ({
    user,
    lastActive: ago(user.lastActiveAt, this.now()),
    initials: user.name.split(' ').map(p => p[0]).join('').slice(0, 2).toUpperCase(),
    color: AVATAR_COLORS[user.id % AVATAR_COLORS.length],
  })));

  // ---------- Dialogs ----------
  lookups = signal<Lookups>(NO_LOOKUPS);
  draft = signal<Draft | null>(null);
  /** Checks the open user form as it is typed */
  readonly form = new FormErrors(() => this.checkDraft());
  readonly fieldErrors = this.form.shown;
  /** A problem the server reported that is not about one field */
  formError = signal('');
  readonly alert = computed(() => this.formError() || (this.form.any() ? 'Check the highlighted fields' : ''));
  readonly pwForm = new FormErrors(() => ({
    password: check.password(this.newPassword, 'New password'),
    confirm: !this.confirmPassword ? 'Confirm the new password'
      : this.confirmPassword !== this.newPassword ? 'The passwords do not match' : '',
  }));
  saving = signal(false);
  passwordFor = signal<ApiUser | null>(null);
  newPassword = '';
  confirmPassword = '';
  passwordDone = signal(false);
  notice = signal('');

  constructor() {
    toObservable(this.query).pipe(
      debounceTime(200),
      tap(() => this.loading.set(true)),
      switchMap(({ refresh: _, ...params }) => this.http.get<UserList>('/api/users', { params }).pipe(
        tap(() => this.listError.set('')),
        catchError(err => {
          this.listError.set(errorMessage(err, 'Could not load users.'));
          return of(null);
        }),
      )),
      takeUntilDestroyed(),
    ).subscribe(list => {
      if (list) this.list.set(list);
      this.now.set(Date.now());
      this.loading.set(false);
    });
  }

  roleLabel(role: string) {
    return ROLES.find(r => r.value === role)?.label ?? role;
  }

  setStatus(s: StatusFilter) { this.status.set(s); this.pageIndex.set(0); }
  setSearch(q: string) { this.search.set(q); this.pageIndex.set(0); }
  refresh() { this.refreshes.update(n => n + 1); }

  sort(key: SortKey) {
    if (this.sortKey() === key) this.ascending.update(v => !v);
    else { this.sortKey.set(key); this.ascending.set(true); }
    this.pageIndex.set(0);
  }

  ariaSort(key: SortKey) {
    return this.sortKey() !== key ? 'none' : this.ascending() ? 'ascending' : 'descending';
  }

  onPage(e: PageEvent) {
    this.pageSize.set(e.pageSize);
    this.pageIndex.set(e.pageIndex);
  }

  // ---------- Add / edit ----------
  async add() {
    await this.open({
      id: null, employeeCode: '', firstName: '', lastName: '', email: '', mobile: '', role: 'user',
      teamId: null, departmentId: null, branchId: null, managerId: null, shiftId: null,
      timeZone: 'Asia/Kolkata', trackAttendance: true, adminGroups: [], password: '',
    });
  }

  async edit(u: ApiUser) {
    await this.open({
      id: u.id, employeeCode: u.employeeCode, firstName: u.firstName, lastName: u.lastName, email: u.email,
      mobile: u.mobile, role: u.role, teamId: u.team?.id ?? null, departmentId: u.department?.id ?? null,
      branchId: u.branch?.id ?? null, managerId: u.manager?.id ?? null, shiftId: u.shift?.id ?? null, timeZone: u.timeZone,
      trackAttendance: u.trackAttendance, adminGroups: [...u.adminGroups], password: '',
    });
  }

  private async open(draft: Draft) {
    this.form.reset();
    this.formError.set('');
    try {
      const lookups = await firstValueFrom(this.http.get<Lookups>('/api/lookups'));
      this.lookups.set(lookups);
      // An archived team or deactivated manager is not in the lists: the form would show "None" yet
      // save the old id, so clear it to match what is shown
      if (draft.teamId !== null && !lookups.teams.some(t => t.id === draft.teamId)) draft.teamId = null;
      if (draft.managerId !== null && !lookups.managers.some(m => m.id === draft.managerId)) draft.managerId = null;
      this.draft.set(draft);
    } catch (err) {
      this.notice.set(errorMessage(err, 'Could not open the form.'));
    }
  }

  /** Your own role can't be changed from here */
  roleLocked = computed(() => this.draft()?.id === this.auth.user()?.id);
  managers = computed(() => this.lookups().managers.filter(m => m.id !== this.draft()?.id));

  private checkDraft(): Errors {
    const d = this.draft();
    if (!d) return {};
    return {
      firstName: check.personName(d.firstName, 'First name'),
      lastName: check.personName(d.lastName, 'Last name'),
      employeeCode: check.employeeCode(d.employeeCode),
      email: check.email(d.email),
      password: d.id === null ? check.password(d.password) : '',
      mobile: check.mobile(d.mobile),
      timeZone: check.timeZone(d.timeZone),
      managerId: d.id !== null && d.managerId === d.id ? "A user can't be their own manager" : '',
    };
  }

  /** Stores a typed value and checks it straight away */
  set(d: Draft, field: 'firstName' | 'lastName' | 'employeeCode' | 'email' | 'password' | 'mobile' | 'timeZone', value: string) {
    d[field] = value;
    this.formError.set('');
    this.form.touch(field);
  }

  /** Stores a typed password in the reset form and checks it straight away */
  setPw(field: 'newPassword' | 'confirmPassword', value: string) {
    this[field] = value;
    this.formError.set('');
    this.pwForm.touch(field === 'newPassword' ? 'password' : 'confirm');
  }

  closePassword() {
    this.newPassword = '';
    this.confirmPassword = '';
    this.passwordFor.set(null);
  }

  /** Mobile takes numbers only: anything else typed or pasted is dropped, with a note why */
  setMobile(d: Draft, input: HTMLInputElement) {
    const digits = input.value.replace(/\D/g, '').slice(0, check.MOBILE_MAX_DIGITS);
    const dropped = digits !== input.value;
    if (dropped) input.value = digits;
    this.set(d, 'mobile', digits);
    if (dropped) this.form.setServerField('mobile', 'Only numbers are allowed');
  }

  toId(value: string) {
    return value ? Number(value) : null;
  }

  toggleGroup(d: Draft, teamId: number, on: boolean) {
    d.adminGroups = on ? [...d.adminGroups, teamId] : d.adminGroups.filter(t => t !== teamId);
  }

  async save() {
    const d = this.draft();
    if (!d || this.saving()) return;

    this.formError.set('');
    if (!this.form.submit()) return;

    const { id, password, ...body } = d;
    this.saving.set(true);
    try {
      if (id === null) await firstValueFrom(this.http.post('/api/users', { ...body, password }));
      else await firstValueFrom(this.http.put(`/api/users/${id}`, body));
      this.draft.set(null);
      this.afterChange(id === null ? 'User added.' : 'Changes saved.');
    } catch (err) {
      const api = err instanceof HttpErrorResponse ? (err.error as ApiError | null) : null;
      this.form.setServer(api?.fields ?? {});
      if (!api?.fields) this.formError.set(errorMessage(err));
    } finally {
      this.saving.set(false);
    }
  }

  cancel() { this.draft.set(null); }

  // ---------- Row actions ----------
  isSelf(u: ApiUser) { return u.id === this.auth.user()?.id; }

  async toggleStatus(u: ApiUser) {
    const status: UserStatus = u.status === 'active' ? 'inactive' : 'active';
    try {
      await firstValueFrom(this.http.put(`/api/users/${u.id}/status`, { status }));
      this.afterChange(`${u.name} is now ${status}.`);
    } catch (err) {
      this.notice.set(errorMessage(err));
    }
  }

  askPassword(u: ApiUser) {
    this.newPassword = '';
    this.confirmPassword = '';
    this.passwordDone.set(false);
    this.pwForm.reset();
    this.formError.set('');
    this.passwordFor.set(u);
  }

  async savePassword() {
    const u = this.passwordFor();
    if (!u || this.saving()) return;
    this.formError.set('');
    if (!this.pwForm.submit()) return;
    this.saving.set(true);
    try {
      await firstValueFrom(this.http.put(`/api/users/${u.id}/password`, { password: this.newPassword }));
      this.newPassword = '';
      this.confirmPassword = '';
      this.passwordDone.set(true);
      this.notice.set(`Password changed for ${u.name}. They have been signed out.`);
    } catch (err) {
      const api = err instanceof HttpErrorResponse ? (err.error as ApiError | null) : null;
      this.pwForm.setServer(api?.fields ?? {});
      if (!api?.fields) this.formError.set(errorMessage(err));
    } finally {
      this.saving.set(false);
    }
  }

  private afterChange(message: string) {
    this.notice.set(message);
    this.refresh();
    // Names, teams and status feed the Employees / Teams panel on other pages
    const me = this.auth.user();
    if (me) this.people.loadPeople(me.id, true).catch(() => undefined);
  }

  @HostListener('document:keydown.escape')
  onEscape() {
    this.draft.set(null);
    this.closePassword();
  }

  // ---------- Export ----------
  async exportCsv() {
    const { page: _p, pageSize: _s, refresh: _r, ...filter } = this.query();
    const users: ApiUser[] = [];
    try {
      for (let page = 1; ; page++) {
        const res = await firstValueFrom(this.http.get<UserList>('/api/users', { params: { ...filter, page, pageSize: 200 } }));
        users.push(...res.items);
        if (users.length >= res.total || !res.items.length) break;
      }
    } catch (err) {
      this.notice.set(errorMessage(err, 'Could not export users.'));
      return;
    }

    const header = ['Id', 'First name', 'Last name', 'Username', 'Mobile', 'Role', 'Manager', 'Team', 'Department', 'Branch', 'Time zone', 'Last Active', 'Status'];
    const rows = users.map(u => [u.employeeCode, u.firstName, u.lastName, u.email, u.mobile, this.roleLabel(u.role), u.manager?.name ?? '',
      u.team?.name ?? '', u.department?.name ?? '', u.branch?.name ?? '', u.timeZone, u.lastActiveAt ?? '', u.status]);
    const csv = [header, ...rows].map(r => r.map(c => `"${String(c).replace(/"/g, '""')}"`).join(',')).join('\n');
    const url = URL.createObjectURL(new Blob([csv], { type: 'text/csv' }));
    const a = document.createElement('a');
    a.href = url;
    a.download = 'users.csv';
    a.click();
    URL.revokeObjectURL(url);
  }
}
