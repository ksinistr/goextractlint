# Architecture principles

These rules govern all changes to this project.

## Purpose

goextractlint is a small CLI tool that highlights Go functions worth
reviewing for Extract Function opportunities.

The criterion is whether a meaningful function name would separate
intent from implementation. Function length alone is not a defect.
Results support human judgment; analyzed source files remain unchanged.

## Boundaries

Do one thing well: analyze functions and emit findings.

Compose with external tools through explicit inputs, text streams,
and exit codes. Leave change selection, result filtering, presentation,
and workflow orchestration to other tools.

Keep stdout machine-readable and diagnostics on stderr.
Support non-interactive use and incremental processing.

## Internal design

Prefer small, focused modules with explicit dependencies and data flow.
Use the simplest design that serves the current task.

Keep transformations pure where practical. Concentrate filesystem,
network, environment, and stream operations in narrow, explicit
boundaries. Pass external resources as dependencies.

Minimize the spread of side effects, rather than optimizing the number
of effectful functions. Do not combine unrelated responsibilities or
introduce abstractions merely to reduce that count.

## Change criteria

Every change must preserve the tool's focused purpose, composability,
and simplicity. Prefer composition with existing tools over expanding
the CLI's responsibilities.

Reducing side effects is a secondary goal and must not compromise
these principles.

## References

- [Function Length](https://martinfowler.com/bliki/FunctionLength.html)
- [Unix philosophy](https://en.wikipedia.org/wiki/Unix_philosophy)
