// Copyright 2017 Google Inc. All rights reserved.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package java

import (
	"strconv"
	"strings"

	"github.com/google/blueprint"
	"github.com/google/blueprint/proptools"

	"android/soong/android"
	"android/soong/remoteexec"
)

type DexProperties struct {
	// If set to true, compile dex regardless of installable.  Defaults to false.
	Compile_dex *bool

	// list of module-specific flags that will be used for dex compiles
	Dxflags []string `android:"arch_variant"`

	// A list of files containing rules that specify the classes to keep in the main dex file.
	Main_dex_rules []string `android:"path"`

	Optimize struct {
		// If false, disable all optimization.  Defaults to true for android_app and
		// android_test_helper_app modules, false for android_test, java_library, and java_test modules.
		Enabled *bool
		// True if the module containing this has it set by default.
		EnabledByDefault bool `blueprint:"mutated"`

		// Whether to continue building even if warnings are emitted.  Defaults to true.
		Ignore_warnings *bool

		// If true, runs R8 in Proguard compatibility mode (default).
		// Otherwise, runs R8 in full mode.
		Proguard_compatibility *bool

		// If true, optimize for size by removing unused code.  Defaults to true for apps,
		// false for libraries and tests.
		Shrink *bool

		// If true, optimize bytecode.  Defaults to false.
		Optimize *bool

		// If true, obfuscate bytecode.  Defaults to false.
		Obfuscate *bool

		// If true, do not use the flag files generated by aapt that automatically keep
		// classes referenced by the app manifest.  Defaults to false.
		No_aapt_flags *bool

		// Flags to pass to proguard.
		Proguard_flags []string

		// Specifies the locations of files containing proguard flags.
		Proguard_flags_files []string `android:"path"`
	}

	// Keep the data uncompressed. We always need uncompressed dex for execution,
	// so this might actually save space by avoiding storing the same data twice.
	// This defaults to reasonable value based on module and should not be set.
	// It exists only to support ART tests.
	Uncompress_dex *bool

	// Exclude kotlinc generate files: *.kotlin_module, *.kotlin_builtins. Defaults to false.
	Exclude_kotlinc_generated_files *bool
}

type dexer struct {
	dexProperties DexProperties

	// list of extra proguard flag files
	extraProguardFlagFiles android.Paths
	proguardDictionary     android.OptionalPath
	proguardUsageZip       android.OptionalPath
}

func (d *dexer) effectiveOptimizeEnabled() bool {
	return BoolDefault(d.dexProperties.Optimize.Enabled, d.dexProperties.Optimize.EnabledByDefault)
}

var d8, d8RE = pctx.MultiCommandRemoteStaticRules("d8",
	blueprint.RuleParams{
		Command: `rm -rf "$outDir" && mkdir -p "$outDir" && ` +
			`mkdir -p $$(dirname $tmpJar) && ` +
			`${config.Zip2ZipCmd} -i $in -o $tmpJar -x '**/*.dex' && ` +
			`$d8Template${config.D8Cmd} ${config.D8Flags} --output $outDir $d8Flags $tmpJar && ` +
			`$zipTemplate${config.SoongZipCmd} $zipFlags -o $outDir/classes.dex.jar -C $outDir -f "$outDir/classes*.dex" && ` +
			`${config.MergeZipsCmd} -D -stripFile "**/*.class" $mergeZipsFlags $out $outDir/classes.dex.jar $in`,
		CommandDeps: []string{
			"${config.D8Cmd}",
			"${config.Zip2ZipCmd}",
			"${config.SoongZipCmd}",
			"${config.MergeZipsCmd}",
		},
	}, map[string]*remoteexec.REParams{
		"$d8Template": &remoteexec.REParams{
			Labels:          map[string]string{"type": "compile", "compiler": "d8"},
			Inputs:          []string{"${config.D8Jar}"},
			ExecStrategy:    "${config.RED8ExecStrategy}",
			ToolchainInputs: []string{"${config.JavaCmd}"},
			Platform:        map[string]string{remoteexec.PoolKey: "${config.REJavaPool}"},
		},
		"$zipTemplate": &remoteexec.REParams{
			Labels:       map[string]string{"type": "tool", "name": "soong_zip"},
			Inputs:       []string{"${config.SoongZipCmd}", "$outDir"},
			OutputFiles:  []string{"$outDir/classes.dex.jar"},
			ExecStrategy: "${config.RED8ExecStrategy}",
			Platform:     map[string]string{remoteexec.PoolKey: "${config.REJavaPool}"},
		},
	}, []string{"outDir", "d8Flags", "zipFlags", "tmpJar", "mergeZipsFlags"}, nil)

var r8, r8RE = pctx.MultiCommandRemoteStaticRules("r8",
	blueprint.RuleParams{
		Command: `rm -rf "$outDir" && mkdir -p "$outDir" && ` +
			`rm -f "$outDict" && rm -rf "${outUsageDir}" && ` +
			`mkdir -p $$(dirname ${outUsage}) && ` +
			`mkdir -p $$(dirname $tmpJar) && ` +
			`${config.Zip2ZipCmd} -i $in -o $tmpJar -x '**/*.dex' && ` +
			`$r8Template${config.R8Cmd} ${config.R8Flags} -injars $tmpJar --output $outDir ` +
			`--no-data-resources ` +
			`-printmapping ${outDict} ` +
			`-printusage ${outUsage} ` +
			`--deps-file ${out}.d ` +
			`$r8Flags && ` +
			`touch "${outDict}" "${outUsage}" && ` +
			`${config.SoongZipCmd} -o ${outUsageZip} -C ${outUsageDir} -f ${outUsage} && ` +
			`rm -rf ${outUsageDir} && ` +
			`$zipTemplate${config.SoongZipCmd} $zipFlags -o $outDir/classes.dex.jar -C $outDir -f "$outDir/classes*.dex" && ` +
			`${config.MergeZipsCmd} -D -stripFile "**/*.class" $mergeZipsFlags $out $outDir/classes.dex.jar $in`,
		Depfile: "${out}.d",
		Deps:    blueprint.DepsGCC,
		CommandDeps: []string{
			"${config.R8Cmd}",
			"${config.Zip2ZipCmd}",
			"${config.SoongZipCmd}",
			"${config.MergeZipsCmd}",
		},
	}, map[string]*remoteexec.REParams{
		"$r8Template": &remoteexec.REParams{
			Labels:          map[string]string{"type": "compile", "compiler": "r8"},
			Inputs:          []string{"$implicits", "${config.R8Jar}"},
			OutputFiles:     []string{"${outUsage}"},
			ExecStrategy:    "${config.RER8ExecStrategy}",
			ToolchainInputs: []string{"${config.JavaCmd}"},
			Platform:        map[string]string{remoteexec.PoolKey: "${config.REJavaPool}"},
		},
		"$zipTemplate": &remoteexec.REParams{
			Labels:       map[string]string{"type": "tool", "name": "soong_zip"},
			Inputs:       []string{"${config.SoongZipCmd}", "$outDir"},
			OutputFiles:  []string{"$outDir/classes.dex.jar"},
			ExecStrategy: "${config.RER8ExecStrategy}",
			Platform:     map[string]string{remoteexec.PoolKey: "${config.REJavaPool}"},
		},
		"$zipUsageTemplate": &remoteexec.REParams{
			Labels:       map[string]string{"type": "tool", "name": "soong_zip"},
			Inputs:       []string{"${config.SoongZipCmd}", "${outUsage}"},
			OutputFiles:  []string{"${outUsageZip}"},
			ExecStrategy: "${config.RER8ExecStrategy}",
			Platform:     map[string]string{remoteexec.PoolKey: "${config.REJavaPool}"},
		},
	}, []string{"outDir", "outDict", "outUsage", "outUsageZip", "outUsageDir",
		"r8Flags", "zipFlags", "tmpJar", "mergeZipsFlags"}, []string{"implicits"})

func (d *dexer) dexCommonFlags(ctx android.ModuleContext,
	minSdkVersion android.SdkSpec) (flags []string, deps android.Paths) {

	flags = d.dexProperties.Dxflags
	// Translate all the DX flags to D8 ones until all the build files have been migrated
	// to D8 flags. See: b/69377755
	flags = android.RemoveListFromList(flags,
		[]string{"--core-library", "--dex", "--multi-dex"})

	for _, f := range android.PathsForModuleSrc(ctx, d.dexProperties.Main_dex_rules) {
		flags = append(flags, "--main-dex-rules", f.String())
		deps = append(deps, f)
	}

	if ctx.Config().Getenv("NO_OPTIMIZE_DX") != "" {
		flags = append(flags, "--debug")
	}

	if ctx.Config().Getenv("GENERATE_DEX_DEBUG") != "" {
		flags = append(flags,
			"--debug",
			"--verbose")
	}

	effectiveVersion, err := minSdkVersion.EffectiveVersion(ctx)
	if err != nil {
		ctx.PropertyErrorf("min_sdk_version", "%s", err)
	}

	flags = append(flags, "--min-api "+strconv.Itoa(effectiveVersion.FinalOrFutureInt()))
	return flags, deps
}

func d8Flags(flags javaBuilderFlags) (d8Flags []string, d8Deps android.Paths) {
	d8Flags = append(d8Flags, flags.bootClasspath.FormRepeatedClassPath("--lib ")...)
	d8Flags = append(d8Flags, flags.dexClasspath.FormRepeatedClassPath("--lib ")...)

	d8Deps = append(d8Deps, flags.bootClasspath...)
	d8Deps = append(d8Deps, flags.dexClasspath...)

	return d8Flags, d8Deps
}

func (d *dexer) r8Flags(ctx android.ModuleContext, flags javaBuilderFlags) (r8Flags []string, r8Deps android.Paths) {
	opt := d.dexProperties.Optimize

	// When an app contains references to APIs that are not in the SDK specified by
	// its LOCAL_SDK_VERSION for example added by support library or by runtime
	// classes added by desugaring, we artifically raise the "SDK version" "linked" by
	// ProGuard, to
	// - suppress ProGuard warnings of referencing symbols unknown to the lower SDK version.
	// - prevent ProGuard stripping subclass in the support library that extends class added in the higher SDK version.
	// See b/20667396
	var proguardRaiseDeps classpath
	ctx.VisitDirectDepsWithTag(proguardRaiseTag, func(m android.Module) {
		dep := ctx.OtherModuleProvider(m, JavaInfoProvider).(JavaInfo)
		proguardRaiseDeps = append(proguardRaiseDeps, dep.HeaderJars...)
	})

	r8Flags = append(r8Flags, proguardRaiseDeps.FormJavaClassPath("-libraryjars"))
	r8Flags = append(r8Flags, flags.bootClasspath.FormJavaClassPath("-libraryjars"))
	r8Flags = append(r8Flags, flags.dexClasspath.FormJavaClassPath("-libraryjars"))

	r8Deps = append(r8Deps, proguardRaiseDeps...)
	r8Deps = append(r8Deps, flags.bootClasspath...)
	r8Deps = append(r8Deps, flags.dexClasspath...)

	flagFiles := android.Paths{
		android.PathForSource(ctx, "build/make/core/proguard.flags"),
	}

	flagFiles = append(flagFiles, d.extraProguardFlagFiles...)
	// TODO(ccross): static android library proguard files

	flagFiles = append(flagFiles, android.PathsForModuleSrc(ctx, opt.Proguard_flags_files)...)

	r8Flags = append(r8Flags, android.JoinWithPrefix(flagFiles.Strings(), "-include "))
	r8Deps = append(r8Deps, flagFiles...)

	// TODO(b/70942988): This is included from build/make/core/proguard.flags
	r8Deps = append(r8Deps, android.PathForSource(ctx,
		"build/make/core/proguard_basic_keeps.flags"))

	r8Flags = append(r8Flags, opt.Proguard_flags...)

	if BoolDefault(opt.Proguard_compatibility, true) {
		r8Flags = append(r8Flags, "--force-proguard-compatibility")
	} else {
		// TODO(b/213833843): Allow configuration of the prefix via a build variable.
		var sourceFilePrefix = "go/retraceme "
		var sourceFileTemplate = "\"" + sourceFilePrefix + "%MAP_ID\""
		// TODO(b/200967150): Also tag the source file in compat builds.
		if Bool(opt.Optimize) || Bool(opt.Obfuscate) {
			r8Flags = append(r8Flags, "--map-id-template", "%MAP_HASH")
			r8Flags = append(r8Flags, "--source-file-template", sourceFileTemplate)
		}
	}

	// TODO(ccross): Don't shrink app instrumentation tests by default.
	if !Bool(opt.Shrink) {
		r8Flags = append(r8Flags, "-dontshrink")
	}

	if !Bool(opt.Optimize) {
		r8Flags = append(r8Flags, "-dontoptimize")
	}

	// TODO(ccross): error if obufscation + app instrumentation test.
	if !Bool(opt.Obfuscate) {
		r8Flags = append(r8Flags, "-dontobfuscate")
	}
	// TODO(ccross): if this is an instrumentation test of an obfuscated app, use the
	// dictionary of the app and move the app from libraryjars to injars.

	// Don't strip out debug information for eng builds.
	if ctx.Config().Eng() {
		r8Flags = append(r8Flags, "--debug")
	}

	// TODO(b/180878971): missing classes should be added to the relevant builds.
	// TODO(b/229727645): do not use true as default for Android platform builds.
	if proptools.BoolDefault(opt.Ignore_warnings, true) {
		r8Flags = append(r8Flags, "-ignorewarnings")
	}

	return r8Flags, r8Deps
}

func (d *dexer) compileDex(ctx android.ModuleContext, flags javaBuilderFlags, minSdkVersion android.SdkSpec,
	classesJar android.Path, jarName string) android.OutputPath {

	// Compile classes.jar into classes.dex and then javalib.jar
	javalibJar := android.PathForModuleOut(ctx, "dex", jarName).OutputPath
	outDir := android.PathForModuleOut(ctx, "dex")
	tmpJar := android.PathForModuleOut(ctx, "withres-withoutdex", jarName)

	zipFlags := "--ignore_missing_files"
	if proptools.Bool(d.dexProperties.Uncompress_dex) {
		zipFlags += " -L 0"
	}

	commonFlags, commonDeps := d.dexCommonFlags(ctx, minSdkVersion)

	// Exclude kotlinc generated files when "exclude_kotlinc_generated_files" is set to true.
	mergeZipsFlags := ""
	if proptools.BoolDefault(d.dexProperties.Exclude_kotlinc_generated_files, false) {
		mergeZipsFlags = "-stripFile META-INF/*.kotlin_module -stripFile **/*.kotlin_builtins"
	}

	useR8 := d.effectiveOptimizeEnabled()
	if useR8 {
		proguardDictionary := android.PathForModuleOut(ctx, "proguard_dictionary")
		d.proguardDictionary = android.OptionalPathForPath(proguardDictionary)
		proguardUsageDir := android.PathForModuleOut(ctx, "proguard_usage")
		proguardUsage := proguardUsageDir.Join(ctx, ctx.Namespace().Path,
			android.ModuleNameWithPossibleOverride(ctx), "unused.txt")
		proguardUsageZip := android.PathForModuleOut(ctx, "proguard_usage.zip")
		d.proguardUsageZip = android.OptionalPathForPath(proguardUsageZip)
		r8Flags, r8Deps := d.r8Flags(ctx, flags)
		r8Deps = append(r8Deps, commonDeps...)
		rule := r8
		args := map[string]string{
			"r8Flags":        strings.Join(append(commonFlags, r8Flags...), " "),
			"zipFlags":       zipFlags,
			"outDict":        proguardDictionary.String(),
			"outUsageDir":    proguardUsageDir.String(),
			"outUsage":       proguardUsage.String(),
			"outUsageZip":    proguardUsageZip.String(),
			"outDir":         outDir.String(),
			"tmpJar":         tmpJar.String(),
			"mergeZipsFlags": mergeZipsFlags,
		}
		if ctx.Config().UseRBE() && ctx.Config().IsEnvTrue("RBE_R8") {
			rule = r8RE
			args["implicits"] = strings.Join(r8Deps.Strings(), ",")
		}
		ctx.Build(pctx, android.BuildParams{
			Rule:            rule,
			Description:     "r8",
			Output:          javalibJar,
			ImplicitOutputs: android.WritablePaths{proguardDictionary, proguardUsageZip},
			Input:           classesJar,
			Implicits:       r8Deps,
			Args:            args,
		})
	} else {
		d8Flags, d8Deps := d8Flags(flags)
		d8Deps = append(d8Deps, commonDeps...)
		rule := d8
		if ctx.Config().UseRBE() && ctx.Config().IsEnvTrue("RBE_D8") {
			rule = d8RE
		}
		ctx.Build(pctx, android.BuildParams{
			Rule:        rule,
			Description: "d8",
			Output:      javalibJar,
			Input:       classesJar,
			Implicits:   d8Deps,
			Args: map[string]string{
				"d8Flags":        strings.Join(append(commonFlags, d8Flags...), " "),
				"zipFlags":       zipFlags,
				"outDir":         outDir.String(),
				"tmpJar":         tmpJar.String(),
				"mergeZipsFlags": mergeZipsFlags,
			},
		})
	}
	if proptools.Bool(d.dexProperties.Uncompress_dex) {
		alignedJavalibJar := android.PathForModuleOut(ctx, "aligned", jarName).OutputPath
		TransformZipAlign(ctx, alignedJavalibJar, javalibJar)
		javalibJar = alignedJavalibJar
	}

	return javalibJar
}
