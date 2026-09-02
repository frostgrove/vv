# UC-027 — Run one codebase as an API, a worker and a seed command

**Actor:** the application author assembling a service that is deployed more than one way
**Covered by:** [[FL-030]]

## Scenario

The author has a handful of bounded contexts and three ways to start the same
binary: an API replica behind a load balancer, a worker fleet that runs the
background half, and a seed command run once after a migration. Today that is
three hand-written lists of wiring, most of it identical, and the difference
between them lives in whoever last edited two of the three. A worker that
stopped being listed keeps compiling, keeps passing tests, and stops running.

The author wants each context to declare once what it contributes and what part
of it belongs to which kind of deployment, wants starting the process a
different way to be a named choice rather than a different list, and wants to be
able to ask what a deployment would be before starting one.

## What must hold

1. A bounded context declares what it contributes in one place: the constructors
   it provides, the API surface it mounts, the background work it runs, the seed
   data it owns and the health checks it publishes.
2. The declaration says which of those belong to which kind of deployment. What
   every deployment carries — plain constructors and health checks — is not
   something the author has to repeat per deployment.
3. Starting the process as an API, as a worker or as a seed command is one named
   choice over one list of contexts. It is not a second list, and adding a
   context does not mean editing three places.
4. A part of a context that the chosen deployment does not run is not wired at
   all: an API replica does not build its workers, and a seed command mounts no
   routes and starts no background work.
5. The author can ask what a chosen deployment would be — which contexts, which
   contributions, which of them active — and get the answer without any of it
   being built: no constructor called, no connection opened.
6. Wiring mistakes are refused with every problem named at once, before anything
   is built: a context with no name or no contributions, two contexts with one
   name, a deployment naming a kind of work that does not exist, and a
   deployment that would activate nothing at all.
7. A shape that is unusual but legitimate — running every kind of work over a
   set of contexts that has no background work in it — is reported and not
   refused.
8. The short form and the explicit form build the same thing and refuse the same
   mistakes; the short form is a convenience over the long one, never the only
   way in.
9. None of this requires a particular container or HTTP router. The declaration
   is an ordinary value; binding it to a container is a separate, optional
   import, and a context that contributes routes does not make the declaration
   depend on a router.

## Out of scope

- Choosing the deployment from a command line, an environment variable or a
  config file. The profile is a value; where it comes from is the application's.
- Summarising what each subsystem needs of its storage — the schema versions and
  migrations a deployment requires. The diagnosis is where that will be
  collected, and today it reports the composition only.
- Ordering start-up beyond the declared order of contributions and the module
  order. What starts background work and in what sequence is [[UC-026]].
- Discovering contexts. The list is a call site somebody wrote; nothing scans a
  package tree.
