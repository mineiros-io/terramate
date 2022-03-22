# Execution Ordering

Sometimes stacks are completely independent of each other, but on
certain occasions it may happen that infrastructure that is created
by **stack-a** is required by **stack-b**, like using the outputs
of **stack-a** as inputs for **stack-b**.

This can be done through data sources or
by [loading the state](https://www.terraform.io/docs/language/state/remote-state-data.html)
of another stack, or or even an implicit dependency like hard coding the name/ID.

Independent on how you approach the problem, you need
an explicit way to communicate that changes on **stack A** affect execution of
**stack B**, so the order of execution of the stacks, if they are
selected for execution, should always be:

* 1 - **stack A**
* 2 - **stack B**

To help with that terramate provides a way to explicit declare
the desired order of execution between stacks.


## Defining Order Of Execution

Order of execution is declared inside the **stack** block using the
field **before** and **after**. 

Each field is a set of strings (**set(string)**),
where each string is a reference to another stack.

References to stacks can be relative to the stack being configured in the form:

```
../../stack
```

Or they can be relative to the project root, starting with "/":

```
/path/relative/to/project/root/stack
```

**before** ensures that the configured stack is executed before the
listed stacks, as the stack you are saying "I execute before these stacks".

**after** ensures the opposite, that the listed stacks are executed before
the configured stack, you are saying "I execute after these stacks".

For example, let's assume we have a project organized like this:

```
.
├── stack-a
│   └── terramate.tm.hcl
└── stack-b
    └── terramate.tm.hcl
```

And **stack-a/terramate.tm.hcl** looks like:


```hcl
terramate {
    required_version = "<version>"
}
```


And then we have **stack-b/terramate.tm.hcl**:


```hcl
terramate {
    required_version = "<version>"
}

stack {
    after = [
        "../stack-a"
    ]
}
```

That can also be defined by using a project root relative path:


```hcl
terramate {
    required_version = "<version>"
}

stack {
    after = [
        "/stack-a"
    ]
}
```

For both equivalent configurations, the order of execution will be:

* stack-a
* stack-b

The same order of execution can be defined using **before**:

**stack-a/terramate.tm.hcl**:

```hcl
terramate {
    required_version = "<version>"
}

stack {
    before = [
        "../stack-b"
    ]
}
```

**stack-b/terramate.tm.hcl**:

```hcl
terramate {
    required_version = "<version>"
}
```

This would also be a valid way to express the same order (although redundant):

**stack-a/terramate.tm.hcl**:

```hcl
terramate {
    required_version = "<version>"
}

stack {
    before = [
        "../stack-b"
    ]
}
```

**stack-b/terramate.tm.hcl**:

```hcl
terramate {
    required_version = "<version>"
}

stack {
    after = [
        "../stack-a"
    ]
}
```

You can also use **before** and **after** simultaneously on the same
stack for more complex scenarios. Lets add a third **stack-c** to our example.
The three stacks are defined as follows:

**stack-a/terramate.tm.hcl**:

```hcl
terramate {
    required_version = "<version>"
}
```

**stack-b/terramate.tm.hcl**:

```hcl
terramate {
    required_version = "<version>"
}
```

**stack-c/terramate.tm.hcl**:

```hcl
terramate {
    required_version = "<version>"
}

stack {
    before = [
        "../stack-b"
    ]
    after = [
        "../stack-a"
    ]
}
```

The order of execution will be:

* stack-a
* stack-c
* stack-b

One example of terramate command that leverages order of
execution is **terramate run**.

This will run **terraform** plan on all stacks, but respecting ordering:

```sh
terramate run terraform plan
```


## Change Detection And Ordering

When using any terramate command with support to change detection,
execution order is only imposed on stacks detected as changed. If a stack
is mentioned on **before**/**after** but the mentioned stack has no changes
on it, it will be ignored when calculating order.

An example of such a command would be using terramate to run **terraform apply**,
but only on changes stacks, like this:

```
terramate run --changed terraform apply
```

The overall algorithm for this case:

* Check which stacks have changed, lets call the result a **changeset**
* Ordering is established on top of the previously calculated **changeset**

Given that we have 3 stacks, **stack-a**, **stack-b**, **stack-c**.
**stack-a** has no ordering requisites.
**stack-b** defines this order:

```hcl
stack {
    after = [
        "../stack-a",
    ]
}
```

**stack-c** defines this order:

```hcl
stack {
    after = [
        "../stack-a",
        "../stack-b",
    ]
}
```

The **static** order is defined as:

* stack-a
* stack-b
* stack-c

If the **changeset=('stack-a', 'stack-c')**, this will be the **runtime** order:

* stack-a
* stack-c

Even though **stack-c** defined that it needs to be run after **stack-b**, since
**stack-b** has no changes on it, it will be ignored when defining the
**runtime** order.


## Failure Modes

Currently the behavior when a stack execution fails given a total order of
execution is undefined. Whatever observed behavior should not be relied upon
since it may change on the future.


## What About Cycles/Conflicts ?

If any cycles are detected on the ordering definitions this will be
considered a failure and **terramate** will abort with an
error message pointing out the detected cycle.

Also in the case of a conflict, like a stack defined like this:

**stack-a/terramate.tm.hcl**:

```hcl
terramate {
    required_version = "<version>"
}

stack {
    before = [
        "../stack-b"
    ]
    after = [
        "../stack-b"
    ]
}
```

An error will be reported.