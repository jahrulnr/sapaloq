# TypeScript Style Guide

Based on the Google TypeScript Style Guide, adapted for modern tooling and
React/Vite projects (TypeScript 5.x).

---

## 1. Tooling Setup

**Two tools, both mandatory:** `eslint` (logic + style) and `prettier`
(formatting). They have separate concerns — don't conflate them.

### Prettier config (`.prettierrc`)
```json
{
  "semi": true,
  "singleQuote": true,
  "trailingComma": "all",
  "printWidth": 100,
  "tabWidth": 2,
  "arrowParens": "always"
}
```

### ESLint config (`eslint.config.js` — flat config, ESLint 9+)
```js
import js from '@eslint/js';
import tseslint from 'typescript-eslint';
import prettier from 'eslint-config-prettier';

export default tseslint.config(
  js.configs.recommended,
  ...tseslint.configs.strictTypeChecked,
  prettier,
  {
    languageOptions: {
      parserOptions: {
        project: true,
        tsconfigRootDir: import.meta.dirname,
      },
    },
    rules: {
      '@typescript-eslint/no-explicit-any': 'error',
      '@typescript-eslint/no-unused-vars': ['error', { argsIgnorePattern: '^_' }],
      '@typescript-eslint/consistent-type-imports': ['error', { prefer: 'type-imports' }],
      'no-console': ['warn', { allow: ['warn', 'error'] }],
    },
  },
);
```

### tsconfig.json (strict baseline)
```json
{
  "compilerOptions": {
    "target": "ES2022",
    "module": "ESNext",
    "moduleResolution": "bundler",
    "lib": ["ES2022", "DOM"],
    "strict": true,
    "noUncheckedIndexedAccess": true,
    "noImplicitOverride": true,
    "exactOptionalPropertyTypes": true,
    "useUnknownInCatchVariables": true,
    "skipLibCheck": true,
    "esModuleInterop": true,
    "paths": {
      "@/*": ["./src/*"]
    }
  },
  "include": ["src"]
}
```

`"strict": true` enables: `strictNullChecks`, `strictFunctionTypes`,
`noImplicitAny`, `noImplicitThis`, `alwaysStrict` — all at once. Never
disable `strict`.

---

## 2. Language Features

### Variables
- Use `const` by default. Use `let` only when reassignment is necessary.
- `var` is **forbidden**. No exceptions.

### Types
- Avoid `any`. Use `unknown` for truly unknown input, then narrow it.
  ```ts
  // ❌
  function parse(data: any) { return data.name; }

  // ✅
  function parse(data: unknown): string {
    if (typeof data !== 'object' || data === null) throw new Error('invalid');
    if (!('name' in data) || typeof (data as { name: unknown }).name !== 'string') {
      throw new Error('missing name');
    }
    return (data as { name: string }).name;
  }
  ```
- Avoid `{}` — use `Record<string, unknown>` or `object`.
- Prefer `interface` for object shapes, `type` for unions/intersections/mapped types.
  ```ts
  interface User { id: string; name: string; }      // ✅ extendable object shape
  type Status = 'active' | 'inactive' | 'pending';  // ✅ union
  type PartialUser = Partial<User>;                  // ✅ mapped type
  ```
- Use `T[]` for simple arrays, `Array<T>` for complex union element types.
  ```ts
  string[]                  // ✅ simple
  Array<string | number>    // ✅ union element
  ```

### Imports & Exports
- Use **named exports** as the default.
  ```ts
  export { UserService };               // ✅
  export default UserService;           // ❌ (except React components — see below)
  ```
- **Exception for React components:** default exports are acceptable for
  page-level components and when required by the framework (React.lazy,
  Next.js pages, Vite HMR). Utility functions, hooks, and services always
  use named exports.
- Use `import type` for type-only imports (enforced by ESLint rule above):
  ```ts
  import type { User } from './types';  // ✅
  import { User } from './types';       // ❌ if User is only a type
  ```
- Use path aliases instead of relative `../../../`:
  ```ts
  import { userService } from '@/services/user'; // ✅
  import { userService } from '../../../services/user'; // ❌
  ```

### Classes
- Use TypeScript's `private` modifier, not JavaScript's `#private` fields.
- Mark constructor-only assigned properties `readonly`.
- Omit `public` — it's the default.
- Use `protected` for intended subclass access.

### Functions
- Named functions: use `function` declaration.
- Callbacks and inline functions: use arrow functions.
- Async functions: prefer `async/await` over raw `.then()` chains.
  ```ts
  // ✅
  async function fetchUser(id: string): Promise<User> {
    const res = await api.get(`/users/${id}`);
    return res.data;
  }

  // ❌ (harder to read, harder to debug)
  function fetchUser(id: string): Promise<User> {
    return api.get(`/users/${id}`).then(res => res.data);
  }
  ```

### Strings
- Single quotes `'` for string literals.
- Template literals `` ` `` for interpolation and multi-line strings.

### Equality
- Always use `===` and `!==`. Never `==` or `!=`.

---

## 3. Naming

| Kind | Convention | Example |
|---|---|---|
| Classes, interfaces, types, enums | `UpperCamelCase` | `UserService`, `ApiResponse` |
| Variables, functions, methods, params | `lowerCamelCase` | `fetchUser`, `isActive` |
| Global constants, enum values | `CONSTANT_CASE` | `MAX_RETRIES`, `Status.ACTIVE` |
| React components | `UpperCamelCase` | `UserCard`, `AuthProvider` |
| Custom hooks | `useCamelCase` | `useAuth`, `useFetchUser` |
| Files (non-component) | `kebab-case.ts` | `user-service.ts` |
| Files (React component) | `PascalCase.tsx` | `UserCard.tsx` |

- No `_` prefix/suffix for private members — use `private` modifier instead.

---

## 4. Type System Best Practices

- **Prefer strict null handling.** Always check for `null`/`undefined` before
  accessing properties when `strictNullChecks` is on.
- **Prefer optional `?` over `| undefined`** for optional fields:
  ```ts
  interface Opts { timeout?: number; }       // ✅
  interface Opts { timeout: number | undefined; } // ❌
  ```
- **Don't use non-null assertion `!`** unless you can prove the value is
  never null at that point — and add a comment if you do.
- **Avoid type assertions `as`** unless bridging from untyped external data
  (e.g. API responses). Add a comment explaining why the assertion is safe.

---

## 5. Comments & Documentation

- **JSDoc** (`/** */`) for public API documentation.
- **Inline comments** (`//`) for implementation notes.
- Don't repeat type information in `@param` / `@return` — TypeScript already
  has it. Only document the *semantics*.
  ```ts
  /**
   * Fetches a user by their unique identifier.
   * Returns null if the user does not exist (does not throw).
   */
  async function getUser(id: string): Promise<User | null> { ... }
  ```

---

## 6. Vite-Specific Notes

- Configure path aliases in both `tsconfig.json` (`paths`) and `vite.config.ts`
  (`resolve.alias`) — they must stay in sync.
- Use `import.meta.env` for environment variables. Prefix custom vars with
  `VITE_` to expose them to client code.
- Don't use `process.env` in client-side code.
- Use `import.meta.glob` for bulk imports (e.g. auto-loading route modules).

*Primary source: [Google TypeScript Style Guide](https://google.github.io/styleguide/tsguide.html),
[TypeScript Handbook](https://www.typescriptlang.org/docs/handbook/)*
