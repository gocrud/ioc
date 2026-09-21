## gocrud/di

`di` 是一个 Go 依赖注入框架，只提供 **Singleton** 一种生命周期：`ServiceCollection.Build()` 会一次性完成依赖图分析、循环依赖检测，并按依赖顺序把所有服务**提前构造好**；之后的 `Resolve`/`ResolveAll`/`ResolveKeyed` 只是读取已经构建好的值，不再有任何反射调用、锁或递归，因此解析接近 O(1)。

核心类型：

| 类型/函数 | 说明 |
| --- | --- |
| `ServiceCollection` / `NewServiceCollection()` | 注册服务的容器，链式调用 `AddSingleton`/`Configure`/`Decorate` 等方法 |
| `*ServiceProvider` / `sc.Build()` | 解析服务的只读容器，`Build()` 返回时全部单例已构造完成 |
| `AddKeyedSingleton` + `ResolveKeyed` | 具名服务：同一类型按 key 区分多个实现 |
| `io.Closer` / `provider.Close()` | 单例的资源释放钩子 |
| `*Options[T]` | `Configure[T]` 产出的只读配置包装 |
| `*OptionsMonitor[T]` | `ConfigureMonitor[T]` 产出的可运行时更新配置 |
| `ServiceCollectionExtension` + `sc.Extend(...)` | 第三方/业务代码编写可复用注册扩展的约定 |

要求 Go 1.27+（依赖方法级泛型参数，如 `sc.AddSingleton[Logger](...)`）。

---

### 目录

1. [快速开始](#1-快速开始)
2. [注册服务：AddSingleton](#2-注册服务)
3. [两种 provider 形态：实例 / 自动装配构造函数](#3-两种-provider-形态)
4. [解析服务](#4-解析服务)
5. [资源释放：io.Closer 与 Close](#5-资源释放io-closer-与-close)
6. [TryAdd 系列：幂等注册](#6-tryadd-系列)
7. [Options 模式：Configure](#7-options-模式)
8. [Decorate 装饰器](#8-decorate-装饰器)
9. [ServiceCollection 扩展机制](#9-servicecollection-扩展机制)
10. [Build：依赖图、循环依赖检测与 O(1) 解析](#10-build依赖图循环依赖检测与-o1-解析)
11. [Keyed Services 具名服务](#11-keyed-services-具名服务)
12. [错误处理](#12-错误处理)
13. [完整示例](#13-完整示例)
14. [已知限制](#14-已知限制)

---

### 1. 快速开始

```go
import "github.com/gocrud/di"

type Logger interface{ Log(string) }
type consoleLogger struct{}

func (consoleLogger) Log(msg string) { fmt.Println(msg) }
func NewConsoleLogger() Logger        { return consoleLogger{} }

type Repository struct{ Logger Logger }

func NewRepository(logger Logger) *Repository { return &Repository{Logger: logger} }

func main() {
    sc := di.NewServiceCollection()
    sc.AddSingleton[Logger](NewConsoleLogger)
    sc.AddSingleton[*Repository](NewRepository) // 依赖 Logger，构造顺序自动排在它之后

    provider, err := sc.Build() // 所有单例在这里被静态分析、检测循环依赖并提前构造完成
    if err != nil {
        panic(err)
    }
    defer provider.Close()

    repo := provider.MustResolve[*Repository]()
    repo.Logger.Log("server started")
}
```

三步走：`NewServiceCollection()` 注册 → `Build()` 一次性构造 → `Resolve`/`MustResolve` 只读取。

---

### 2. 注册服务

```go
sc := di.NewServiceCollection()

sc.AddSingleton[Logger](NewConsoleLogger)     // 整个 provider 生命周期内只创建一次
sc.AddSingleton[Repository](NewSqlRepository) // 依赖 Logger，构造顺序自动排在它之后
```

- `AddSingleton` 是 `*ServiceCollection` 上的泛型方法，`TService` 写在 `[...]` 里，可以是接口或具体类型。
- 返回 `*ServiceCollection`，支持链式调用：`sc.AddSingleton[A](...).AddSingleton[B](...)`。
- **注意**：同一个 `TService` 可以多次注册（用于多实现场景，见 [ResolveAll](#4-解析服务)）；`Resolve`/`MustResolve` 只返回**最后一次注册**的实现。
- **只提供 Singleton**：没有 Scoped/Transient。因为所有依赖都在 `Build()` 时静态展开、一次性构造完成，"每次请求一份"这类需求应该由业务代码自己在 Singleton 之上按需创建（例如每个请求手动 `new` 一个短生命周期对象），而不是让容器分场景缓存实例。

---

### 3. 两种 provider 形态

`AddSingleton[T](provider any)` 的 `provider` 参数在注册时通过反射自动侦测形态：

| 形态 | 写法 | 说明 |
| --- | --- | --- |
| 预构建实例 | `sc.AddSingleton[Logger](myLoggerInstance)` | 非函数值，直接作为实例使用 |
| 自动装配构造函数 | `sc.AddSingleton[Repository](NewSqlRepository)` | 任意签名 `func(depA, depB, ...) TImpl` 或 `(TImpl, error)`；每个参数类型都必须是另一个已注册的服务，`Build()` 时递归解析并按依赖顺序调用 |

```go
// 自动装配：NewSqlRepository 的每个参数都会被静态解析到对应依赖
func NewSqlRepository(logger Logger, cfg *AppConfig) *SqlRepository { ... }
sc.AddSingleton[Repository](NewSqlRepository)

// 预构建实例
sc.AddSingleton[Clock](RealClock{})
```

**注意事项**：
- provider 函数可以返回 `(TImpl)` 或 `(TImpl, error)`；`TImpl` 必须能赋值给 `TService`（相同类型或实现了该接口）。
- **不支持手动工厂**（即接收容器本身、在函数体内自行调用 `Resolve` 的写法）：构造函数只能通过参数类型声明依赖，这样所有依赖关系在 `Build()` 时都是静态可分析的，才能做到提前构造 + O(1) 解析。
- **签名不合法（如返回值个数不对、返回类型不满足接口）会在注册时直接 `panic`**——这是配置期错误，不是运行时数据错误，因此不通过 `error` 返回，方便尽早发现。

---

### 4. 解析服务

```go
provider, err := sc.Build() // 所有单例已在这里构造完成

logger, err := provider.Resolve[Logger]()          // 返回 (T, error)
logger := provider.MustResolve[Logger]()            // 出错直接 panic
validators, err := provider.ResolveAll[Validator]() // 按注册顺序返回全部实现
```

- `Resolve[T]`/`MustResolve[T]`/`ResolveAll[T]` 是 `*ServiceProvider` 上的泛型方法。
- 未注册的服务返回 `di.ErrServiceNotRegistered`（可用 `errors.Is` 判断）。
- 由于所有实例都在 `Build()` 时已经构造好，这三个方法只是一次 map/切片查找，不会再触发任何构造、反射调用或加锁。

---

### 5. 资源释放：io.Closer 与 Close

```go
provider, err := sc.Build()
if err != nil {
    panic(err)
}
defer provider.Close() // 进程退出前统一释放

db := provider.MustResolve[*Db]()
```

- 只要某个单例的具体类型（或它内嵌/实现的类型）满足 `io.Closer`（`Close() error`），`Build()` 构造完它时就会自动登记，无需手动注册。
- `provider.Close()` 按**构造顺序的逆序**关闭（依赖方先于被依赖方关闭），并且是幂等的：多次调用只有第一次真正生效，后续调用直接返回第一次的结果。
- 不需要清理资源的服务，什么都不用做——不实现 `Close() error` 就不会被登记。

---

### 6. TryAdd 系列

```go
sc.AddSingleton[Logger](NewConsoleLogger)
sc.TryAddSingleton[Logger](NewFileLogger) // Logger 已注册过，本次调用会被跳过
```

- `TryAddSingleton` 只在 `TService` **完全没有任何注册**时才会真正注册，否则直接返回 `sc` 不做任何事。Keyed 版本 `TryAddKeyedSingleton` 同理，只是判断维度是 `(TService, key)` 这一对组合，见 [第 11 节](#11-keyed-services-具名服务)。
- 典型用途：编写可重复调用的[扩展函数](#9-servicecollection-扩展机制)时，用 `TryAddSingleton` 保证多次调用同一个扩展是安全、幂等的。

---

### 7. Options 模式

```go
type AppConfig struct {
    Port int
}

sc.Configure[AppConfig](func(c *AppConfig) { c.Port = 8080 })
sc.Configure[AppConfig](func(c *AppConfig) { c.Port += 1 }) // 多次 Configure 按注册顺序依次应用

provider, _ := sc.Build()
opts := provider.MustResolve[*di.Options[AppConfig]]()
fmt.Println(opts.Value.Port) // 8081
```

- `Configure[T]` 收集针对同一个 `T` 的多个配置回调，在 `Build()` 时按注册顺序依次应用到一个零值 `T` 上，最终包装为 `*di.Options[T]{Value: T}`（Singleton）。
- 消费方通过 `Resolve[*di.Options[T]]()`/`MustResolve[*di.Options[T]]()` 拿到配置结果，取 `.Value` 字段。
- **注意**：必须在 `Build()` 之前调用 `Configure`；`Build()` 之后再调用不保证生效。

#### OptionsMonitor：运行时可更新的配置

```go
sc.ConfigureMonitor[AppConfig](func(c *AppConfig) { c.Port = 8080 })

provider, _ := sc.Build()
monitor := provider.MustResolve[*di.OptionsMonitor[AppConfig]]()

cancel := monitor.OnChange(func(c AppConfig) {
    fmt.Println("config changed, new port:", c.Port)
})
defer cancel()

// 业务代码自行决定何时/如何拿到新配置（文件 watch、SIGHUP、etcd 等），拿到后手动推送：
monitor.Set(AppConfig{Port: 9090})
fmt.Println(monitor.CurrentValue().Port) // 9090
```

- `ConfigureMonitor[T]` 与 `Configure[T]` 平行：`Build()` 时同样把已注册的回调按顺序应用到零值 `T` 上，作为**初始值**，包装成 Singleton `*di.OptionsMonitor[T]`（与 `*di.Options[T]` 是两个独立类型，可以同时注册同一个 `T`）。
- `OptionsMonitor[T]` 是**可变**的：`CurrentValue()` 无锁读取当前值；`OnChange(listener)` 注册回调并返回取消订阅函数；`Set(newValue)` 更新当前值并按注册顺序通知所有监听器。
- **框架本身不会自动感知配置变化**：没有内置的文件/配置源 watch 机制，`Set` 必须由调用方在拿到新配置后主动调用（例如自己起一个 goroutine 监听文件变化）。本框架只提供“持有当前值 + 发布订阅”这一半机制，不提供“感知配置源变化”的机制。
- 监听器的调用**不持有内部锁**，可以在监听器里安全地再次调用 `OnChange`/`Set`，但监听器本身仍需自行保证并发安全（多次 `Set` 之间没有互斥保证调用顺序与业务逻辑正确性）。

---

### 8. Decorate 装饰器

```go
package main

import (
    "fmt"
    "log"
    "os"
    "time"

    "github.com/gocrud/di"
)

type Logger interface {
    Log(message string)
}

type consoleLogger struct {
    logger *log.Logger
}

func NewConsoleLogger() *consoleLogger {
    return &consoleLogger{logger: log.New(os.Stdout, "", 0)}
}

func (l *consoleLogger) Log(message string) {
    l.logger.Println(message)
}

// timestampLogger 只增加时间戳，具体日志输出仍然交给 inner。
type timestampLogger struct {
    inner Logger
}

func (l *timestampLogger) Log(message string) {
    l.inner.Log(fmt.Sprintf("[%s] %s", time.Now().Format(time.RFC3339), message))
}

func main() {
    services := di.NewServiceCollection()
    services.AddSingleton[Logger](NewConsoleLogger)

    // decorator 接收原始 Logger，并返回包装后的 Logger。
    services.Decorate[Logger](func(inner Logger) (Logger, error) {
        return &timestampLogger{inner: inner}, nil
    })

    provider, err := services.Build()
    if err != nil {
        panic(err)
    }
    defer provider.Close()

    logger := provider.MustResolve[Logger]()
    logger.Log("server started")
}
```

运行后，输出类似：

```text
[2026-08-22T12:00:00+08:00] server started
```

decorator 的第一个参数固定接收原始实例，之后还可以声明任意数量的额外依赖，像构造函数一样被自动装配：

```go
sc.AddSingleton[Logger](NewConsoleLogger)
sc.AddSingleton[*AppConfig](func() *AppConfig { return &AppConfig{Port: 8080} })

sc.Decorate[Logger](func(inner Logger, cfg *AppConfig) (Logger, error) {
    return &timestampLogger{inner: inner, port: cfg.Port}, nil
})
```

- `Decorate[TService]` 包装 `TService` **最后一次注册**的实现：`Build()` 时先构建出原始实例（`inner`），再交给 decorator 函数（连同它额外声明的依赖一起）得到最终实例。
- decorator 额外声明的依赖同样会被计入依赖图，参与循环依赖检测和构造顺序排序。
- 可以多次调用 `Decorate` 形成装饰链，越晚调用的包装在越外层。
- **注意**：`TService` 必须已经注册，否则 `Decorate` 会直接 `panic`。

---

### 9. ServiceCollection 扩展机制

Go 没有真正的"扩展方法"，用桥接方法 + 函数值模拟 `services.AddXxx()` 风格的可复用注册单元：

```go
// mypkg/httpclient.go —— 第三方/业务代码按约定编写的扩展函数
func AddHttpClient(sc *di.ServiceCollection) *di.ServiceCollection {
    sc.TryAddSingleton[*http.Client](func() *http.Client {
        return &http.Client{Timeout: 30 * time.Second}
    })
    return sc
}

// 使用方
sc.AddSingleton[Logger](NewConsoleLogger).
    Extend(mypkg.AddHttpClient).      // 无独立类型参数的扩展：直接链式挂载
    AddSingleton[Repository](NewSqlRepository)
```

- `type ServiceCollectionExtension func(sc *ServiceCollection) *ServiceCollection`，`sc.Extend(ext)` 只是简单地调用 `ext(sc)` 并返回结果，让扩展函数可以嵌进链式调用。
- 扩展函数只需要用到 `ServiceCollection` 已公开的方法（`AddSingleton`/`TryAddSingleton`/`Configure`/`Decorate` 等）即可实现，框架不提供任何内部/友元 API——**设计原则：所有注册能力必须可通过公开 API 组合实现**。
- **注意**：若扩展函数自身需要独立类型参数（如 `AddDbContext[TContext any](sc, configure)`），因为 Go 泛型函数值不能隐式转换成非泛型的 `ServiceCollectionExtension`，需要用闭包包一层才能塞进 `.Extend()`：
  ```go
  sc.Extend(func(sc *di.ServiceCollection) *di.ServiceCollection {
      return mypkg.AddDbContext[AppDbContext](sc, configure)
  })
  ```
  或者干脆不追求链式，直接 `sc = mypkg.AddDbContext[AppDbContext](sc, configure)`。
- 约定：扩展函数内部应使用 `TryAddSingleton` 保证多次调用同一个扩展是幂等的。

---

### 10. Build：依赖图、循环依赖检测与 O(1) 解析

```go
provider, err := sc.Build()
if err != nil {
    // 缺失依赖（ErrServiceNotRegistered）或循环依赖（*di.CircularDependencyError）
    // 都会在这里被发现，不需要额外的校验选项
    panic(err)
}
```

`Build()` 内部分四步完成：

1. **分配下标**：给每个注册（含 keyed）分配一个稳定的整数下标，构造函数的参数类型此时被静态解析成依赖下标，不再需要在运行时按类型查表。
2. **建图 + 循环检测**：把每个构造函数（以及 Decorate 额外声明）的依赖边组织成有向图，做一次 DFS；发现环直接返回 `*di.CircularDependencyError`，`Error()` 会打印完整依赖链，如 `di: circular dependency detected: *di.A -> *di.B -> *di.A`，不会尝试调用任何构造函数。
3. **拓扑排序**：DFS 得到的后序即构造顺序（依赖先于依赖方构造完成）。
4. **按序真正构造**：依次调用每个构造函数（此时它的所有依赖都已经构造好，直接按下标取值传参），应用 Decorate 装饰链，并登记实现了 `io.Closer` 的实例。

```go
type A struct{ B *B }
type B struct{ A *A }

sc.AddSingleton[*A](func(b *B) *A { return &A{B: b} })
sc.AddSingleton[*B](func(a *A) *B { return &B{A: a} })

_, err := sc.Build() // 直接返回 *di.CircularDependencyError，不会等到 Resolve 才发现
```

- **默认就会校验全部注册项**：不管是缺失依赖还是循环依赖，只要 `Build()` 成功返回，就意味着所有服务都已经无错误地构造完成——没有"注册时看不出问题、用到才报错"的情况。
- 因为这一切都在 `Build()` 时一次性做完，运行时的 `Resolve`/`ResolveAll`/`ResolveKeyed` 才能退化成纯粹的查表操作。

---

### 11. Keyed Services 具名服务

用于给同一个服务类型注册多个按“键”区分的实现：

```go
sc.AddKeyedSingleton[Cache]("redis", NewRedisCache)
sc.AddKeyedSingleton[Cache]("memory", NewMemoryCache)

provider, _ := sc.Build()

redis := provider.MustResolveKeyed[Cache]("redis")   // 按 key 精确解析
memory, err := provider.ResolveKeyed[Cache]("memory")
all, err := provider.ResolveAllKeyed[Cache]("redis")  // 该 key 下的全部注册（同 ResolveAll）
```

- 注册方法：`AddKeyedSingleton[TService](key, provider)`，以及幂等版本 `TryAddKeyedSingleton`（只判断 `(TService, key)` 这一对组合是否已注册，不影响其他 key）。
- 解析方法：`ResolveKeyed[T](key)` / `MustResolveKeyed[T](key)` / `ResolveAllKeyed[T](key)`，均为 `*ServiceProvider` 上的泛型方法。
- `key` 必须是**可比较（comparable）**的值（如 `string`、`int`、自定义枚举类型），因为内部以 `map[any]...` 存储；传入不可比较的值（如 slice、map、func）会在解析时 panic。
- **Keyed 注册与非 Keyed 注册是两套完全独立的空间**：`AddKeyedSingleton[Cache]("redis", ...)` 注册的实例，无法通过 `Resolve[Cache]()`/`MustResolve[Cache]()`/`ResolveAll[Cache]()` 解析到，反之亦然；即使 `key` 恰好是同一个类型也互不可见。
- provider 形态与非 Keyed 注册完全一致（预构建实例 / 自动装配构造函数）；构造函数拿不到自己注册时用的 `key`。
- **注意**：自动装配构造函数只能解析非 Keyed 依赖（按类型从容器解析）；Keyed 依赖只能通过 `provider.ResolveKeyed[Dep](key)` 在拿到 `*ServiceProvider` 之后手动取出，不能作为另一个自动装配构造函数的参数类型自动注入。
- 未注册的 `(T, key)` 组合，`ResolveKeyed` 返回 `di.ErrServiceNotRegistered`；`MustResolveKeyed` 直接 panic；`ResolveAllKeyed` 返回空切片、无 error。
- `Build()` 会同时校验 Keyed 与非 Keyed 的全部注册项。

---

### 12. 错误处理

```go
provider, err := sc.Build()
if err != nil {
    var circularErr *di.CircularDependencyError
    switch {
    case errors.Is(err, di.ErrServiceNotRegistered):
        // 某个构造函数/decorator 依赖了一个从未注册的类型
    case errors.As(err, &circularErr):
        // circularErr.Chain 是完整依赖链，circularErr.Error() 形如
        // "di: circular dependency detected: *main.A -> *main.B -> *main.A"
    default:
        // 构造函数/decorator 自身返回的 error（已被 fmt.Errorf 包装了服务类型信息）
    }
    panic(err)
}

logger, err := provider.Resolve[Logger]() // 未注册也会返回 di.ErrServiceNotRegistered
```

- 所有 `Resolve`/`ResolveKeyed` 系列的“未注册”错误都是对哨兵错误 `di.ErrServiceNotRegistered` 的包装，用 `errors.Is` 判断；`Build()` 阶段的同类错误同样如此。
- 循环依赖用独立类型 `*di.CircularDependencyError` 表达（同时也满足 `errors.Is(err, di.ErrCircularDependency)`），额外携带 `Chain []reflect.Type` 字段供程序化处理或打印诊断信息。
- `MustResolve`/`MustResolveKeyed` 遇到上述错误直接 `panic`，适合“预期一定已注册”的场景；不确定时用非 `Must` 版本处理 `error`。
- 构造函数/decorator 自身返回的 `error` 会在 `Build()` 时被 `fmt.Errorf("di: constructing %s: %w", ...)`/`"di: decorating %s: %w"` 包装后原样返回，可以用 `errors.Unwrap`/`errors.Is` 取出原始错误。
- 注册期的编程错误（provider 不是合法形态、签名不满足 `func(deps...) (T[, error])`、`Decorate` 目标未注册等）**直接 `panic`**，不通过 `error` 返回——这类错误应该在开发阶段就被发现，不需要在业务代码里处理。

---

### 13. 完整示例

```go
package main

import (
    "fmt"
    "net/http"
    "time"

    "github.com/gocrud/di"
)

type Logger interface{ Log(string) }
type consoleLogger struct{}

func (consoleLogger) Log(msg string) { fmt.Println(msg) }
func NewConsoleLogger() Logger        { return consoleLogger{} }

type Repository struct{ Logger Logger }

func NewRepository(logger Logger) *Repository { return &Repository{Logger: logger} }

type AppConfig struct{ Port int }

func main() {
    sc := di.NewServiceCollection()

    sc.AddSingleton[Logger](NewConsoleLogger).
        AddSingleton[*Repository](NewRepository).
        TryAddSingleton[Logger](NewConsoleLogger). // 已注册，跳过
        Configure[AppConfig](func(c *AppConfig) { c.Port = 8080 }).
        Decorate[Logger](func(inner Logger) (Logger, error) {
            return inner, nil // 示例：原样返回，真实场景可包装
        }).
        Extend(func(sc *di.ServiceCollection) *di.ServiceCollection {
            sc.TryAddSingleton[*http.Client](func() *http.Client {
                return &http.Client{Timeout: 30 * time.Second}
            })
            return sc
        })

    provider, err := sc.Build()
    if err != nil {
        panic(err)
    }
    defer provider.Close()

    repo := provider.MustResolve[*Repository]()
    opts := provider.MustResolve[*di.Options[AppConfig]]()
    fmt.Println(repo, opts.Value.Port)
}
```

---

### 14. 已知限制

- 只支持 Singleton：没有 Scoped/Transient，也没有"每次请求一份"这类容器内建机制。
- 不支持手动工厂：构造函数不能拿到容器本身去做运行时才能决定的动态解析，所有依赖必须通过参数类型静态声明。
- `OptionsMonitor[T]` 只提供“持有当前值 + 发布订阅”的机制，不提供“感知配置源变化”的机制：更新必须由调用方手动调用 `Set`，框架不会自动 watch 任何配置源。

