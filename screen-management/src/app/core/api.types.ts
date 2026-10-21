// Shapes returned by the Go backend (see backend/README.md)

export type Role = 'super_admin' | 'manager' | 'user';
export type UserStatus = 'active' | 'inactive';

export type TeamStatus = 'active' | 'inactive';
export interface Team { id: number; name: string; members: number; status: TeamStatus; createdAt: string; }

export interface Ref { id: number; name: string; }

export interface ApiUser {
  id: number;
  employeeCode: string;
  firstName: string;
  lastName: string;
  name: string;
  email: string;
  mobile: string;
  role: Role;
  status: UserStatus;
  team: Ref | null;
  department: Ref | null;
  branch: Ref | null;
  manager: Ref | null;
  shift: Ref | null;             // null: the default shift (9:30, no grace)
  timeZone: string;
  trackAttendance: boolean;
  adminGroups: number[];
  lastActiveAt: string | null;
  desktop: string | null;        // computer running the HR Lens agent
  hasPhoto: boolean;             // profile photo at /api/users/{id}/photo
}

// ---------- Profile (user menu > Profile) ----------
export type DocumentFolder = 'degree' | 'experience' | 'identity';

export interface ProfileDetails {
  primary: { displayName: string; gender: string; dateOfBirth: string; maritalStatus: string; pwd: boolean | null; bloodGroup: string };
  contact: { workMail: string; personalMail: string; mobilePhone: string; workPhone: string; residencePhone: string; skype: string };
  address: { current: string; permanent: string };
  relations: { name: string; relation: string; phone: string }[];
  job: {
    dateOfJoining: string; inProbation: boolean | null; jobTitle: string; jobCategory: string; noticeMonths: number | null;
    workType: string; employmentType: string; practice: string; legalEntity: string;
  };
}

export interface UserDocument {
  id: number;
  folder: DocumentFolder;
  details: Record<string, string>;
  fileName: string | null;
  contentType: string | null;
  size: number | null;
  createdAt: string;
  updatedAt: string;
}

export interface Profile {
  user: ApiUser;
  details: ProfileDetails;
  hasPhoto: boolean;
  updatedAt: string | null;
  joinedAt: string;
  directReports: Ref[];
  documents: UserDocument[];
}

export interface UserList { items: ApiUser[]; total: number; page: number; pageSize: number; }

/** Body for creating or updating a user */
export interface UserInput {
  employeeCode: string;
  firstName: string;
  lastName: string;
  email: string;
  mobile: string;
  role: Role;
  teamId: number | null;
  departmentId: number | null;
  branchId: number | null;
  managerId: number | null;
  shiftId: number | null;
  timeZone: string;
  trackAttendance: boolean;
  adminGroups: number[];
  password?: string;   // create only
}

export interface Lookups {
  roles: { value: Role; label: string }[];
  teams: Ref[];
  departments: Ref[];
  branches: Ref[];
  managers: Ref[];
  shifts: Ref[];
}

/** Administration > Shifts. Signing in after start + grace is marked Late. */
export interface Shift { id: number; name: string; start: string; graceMinutes: number; members: number; createdAt: string; }

export interface Person { id: number; name: string; online: boolean; teamId: number | null; }
export interface PeopleResponse {
  employees: Person[];
  teams: { id: number; name: string; members: Person[] }[];
  withoutTeam: number;
}

/** Error body: a message, plus per-field messages for form problems */
export interface ApiError { error: string; fields?: Record<string, string>; }
