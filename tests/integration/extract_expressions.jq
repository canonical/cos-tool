# Returns the datasource type as a lowercase string, or "" if absent.
def datasource_type:
  (.datasource | if type == "object" then .type else . end) // ""
  | ascii_downcase;

# True when the object's datasource is a Loki datasource.
def is_loki_datasource:
  datasource_type | contains("loki");

# True when an expression string looks like LogQL:
# a closing brace followed by a pipeline operator (e.g. |= "x", | json).
def is_logql_syntax(expr):
  expr | type == "string" and test("\\}\\s*\\|");

# Extract the expression string from an object (prefers "expr" over "query").
def expression:
  if has("expr") then .expr else .query end;

# PromQL expressions: has "expr", not a Loki datasource, no LogQL pipeline syntax.
def promql_expressions:
  [ .. | objects
  | select(
      has("expr")
      and (.expr | type) == "string" and .expr != ""
      and (is_loki_datasource | not)
      and (is_logql_syntax(.expr) | not)
    )
  | .expr
  ] | .[];

# LogQL expressions: from a Loki datasource OR recognised by LogQL pipeline syntax.
def logql_expressions:
  [ .. | objects
  | select(
      (has("expr") or has("query"))
      and (
        is_loki_datasource
        or is_logql_syntax(expression)
      )
    )
  | expression
  | select(type == "string" and . != "")
  ] | .[];

if $mode == "promql" then promql_expressions
else logql_expressions
end
