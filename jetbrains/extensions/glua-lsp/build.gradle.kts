plugins {
    id("java")
    id("org.jetbrains.intellij.platform") version "2.17.0"
}

group = providers.gradleProperty("pluginGroup").get()
version = providers.gradleProperty("pluginVersion").get()

repositories {
    maven("https://maven.aliyun.com/repository/public")
    maven("https://maven.aliyun.com/repository/central")
    mavenCentral()
    intellijPlatform {
        defaultRepositories()
    }
}

dependencies {
    implementation("com.google.code.gson:gson:2.10.1")
    intellijPlatform {
        intellijIdea(providers.gradleProperty("platformVersion"))
        bundledModule("intellij.platform.dap")
    }
}

intellijPlatform {
    pluginConfiguration {
        ideaVersion {
            sinceBuild = providers.gradleProperty("pluginSinceBuild")
            untilBuild = provider { null }
        }
    }
}

tasks {
    withType<JavaCompile> {
        sourceCompatibility = "21"
        targetCompatibility = "21"
    }

    buildSearchableOptions {
        enabled = false
    }
}
